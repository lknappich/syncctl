// Package secretsparity compares /etc/gitlab/gitlab-secrets.json between two
// hosts. GitLab's backup documentation says this file must be kept as a
// whole (otherwise e.g. 2FA users cannot sign in after a restore), so
// comparing db_key_base alone is not enough.
//
// The package never decrypts anything and never exposes secret values. The
// file is read over SSH, parsed in memory, flattened to dotted key paths and
// each leaf is reduced to an HMAC-SHA256 digest under a random per-run key.
// Digests are compared inside the process only; they are not part of the
// result, so they are useless outside it. The Result carries key paths only.
package secretsparity

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"

	"github.com/lknappich/syncctl/internal/sshexec"
)

// SecretsCmd is the remote command used to read the file. It is a
// root-owned file, hence sudo (see SECURITY.md sudo table).
const SecretsCmd = "sudo cat /etc/gitlab/gitlab-secrets.json"

// Result lists key paths only. It never contains values or digests.
type Result struct {
	Differ []string // present on both hosts with different values
	OnlyA  []string // present only on host A
	OnlyB  []string // present only on host B
}

// Equal reports whether both files have identical key paths and values.
func (r Result) Equal() bool {
	return len(r.Differ) == 0 && len(r.OnlyA) == 0 && len(r.OnlyB) == 0
}

// Compare reads gitlab-secrets.json from hostA and hostB through runner and
// compares them per key by keyed digest. Errors never contain file content.
func Compare(ctx context.Context, runner sshexec.Runner, hostA, hostB string) (Result, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return Result{}, errors.New("secretsparity: cannot generate digest key")
	}
	defer clear(key)

	a, err := digests(ctx, runner, hostA, key)
	if err != nil {
		return Result{}, fmt.Errorf("host A: %w", err)
	}
	b, err := digests(ctx, runner, hostB, key)
	if err != nil {
		return Result{}, fmt.Errorf("host B: %w", err)
	}

	var res Result
	for p, da := range a {
		db, ok := b[p]
		switch {
		case !ok:
			res.OnlyA = append(res.OnlyA, p)
		case !hmac.Equal(da, db):
			res.Differ = append(res.Differ, p)
		}
	}
	for p := range b {
		if _, ok := a[p]; !ok {
			res.OnlyB = append(res.OnlyB, p)
		}
	}
	sort.Strings(res.Differ)
	sort.Strings(res.OnlyA)
	sort.Strings(res.OnlyB)
	return res, nil
}

// digests fetches and parses one host's file into path -> keyed digest.
// Neither the runner's error text nor parser errors are propagated, because
// either could echo file content; only a generic description is returned.
func digests(ctx context.Context, runner sshexec.Runner, host string, key []byte) (map[string][]byte, error) {
	if err := sshexec.CheckHost(host); err != nil {
		return nil, err
	}
	raw, err := runner.CombinedOutput(ctx, host, SecretsCmd)
	defer clear(raw) // best effort: wipe the raw bytes once parsed
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("reading gitlab-secrets.json failed (check ssh access and sudo for cat)")
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return nil, errors.New("gitlab-secrets.json is not valid JSON")
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("gitlab-secrets.json is not valid JSON")
	}
	obj, ok := root.(map[string]any)
	if !ok {
		return nil, errors.New("gitlab-secrets.json: top level is not an object")
	}
	out := make(map[string][]byte)
	flatten("", obj, key, out)
	return out, nil
}

func flatten(path string, v any, key []byte, out map[string][]byte) {
	switch t := v.(type) {
	case map[string]any:
		if len(t) == 0 && path != "" {
			out[path] = digest(key, "e", "{}")
			return
		}
		for k, c := range t {
			p := k
			if path != "" {
				p = path + "." + k
			}
			flatten(p, c, key, out)
		}
	case []any:
		if len(t) == 0 {
			out[path] = digest(key, "e", "[]")
			return
		}
		for i, c := range t {
			flatten(path+"["+strconv.Itoa(i)+"]", c, key, out)
		}
	case string:
		out[path] = digest(key, "s", t)
	case json.Number:
		out[path] = digest(key, "n", t.String())
	case bool:
		out[path] = digest(key, "b", strconv.FormatBool(t))
	case nil:
		out[path] = digest(key, "z", "")
	}
}

// digest is HMAC-SHA256(key, type tag || value). The tag keeps "1" and 1
// distinct.
func digest(key []byte, tag, val string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(tag))
	m.Write([]byte(val))
	return m.Sum(nil)
}
