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
//
// Values are compared by their JSON literal, so numerically-equal literals
// such as 1 and 1.0 count as different. Memory wiping of raw bytes is best
// effort only: the JSON decoder, Go strings and HMAC internal pads are not
// wiped.
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
	"hash"
	"sort"
	"strconv"
	"unicode"

	"github.com/lknappich/syncctl/internal/sshexec"
)

// SecretsCmd is the remote command used to read the file. It is a
// root-owned file, hence sudo -n (fail fast if the sudoers rule is missing;
// see the SECURITY.md sudo table).
const SecretsCmd = "sudo -n cat /etc/gitlab/gitlab-secrets.json"

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
	raw, err := runner.Output(ctx, host, SecretsCmd)
	defer clear(raw) // best effort: wipes only this buffer, not parser copies
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("reading gitlab-secrets.json failed (check ssh access and sudo for cat)")
	}

	// json.Valid rejects bad syntax and trailing data; the decoder is then
	// only used for UseNumber, which keeps number literals exact (plain
	// Unmarshal would round them through float64).
	if !json.Valid(raw) {
		return nil, errors.New("gitlab-secrets.json is not valid JSON")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return nil, errors.New("gitlab-secrets.json is not valid JSON")
	}
	obj, ok := root.(map[string]any)
	if !ok {
		return nil, errors.New("gitlab-secrets.json: top level is not an object")
	}
	f := &flattener{mac: hmac.New(sha256.New, key), out: make(map[string][]byte)}
	if err := f.walk("", obj); err != nil {
		return nil, err
	}
	return f.out, nil
}

var (
	errBadKey    = errors.New("gitlab-secrets.json: a key contains control or non-printable characters")
	errCollision = errors.New("gitlab-secrets.json: two entries flatten to the same key path")
)

type flattener struct {
	mac hash.Hash
	out map[string][]byte
}

func (f *flattener) set(path, tag, val string) error {
	if _, dup := f.out[path]; dup {
		return errCollision
	}
	f.mac.Reset()
	f.mac.Write([]byte(tag))
	f.mac.Write([]byte(val))
	f.out[path] = f.mac.Sum(nil)
	return nil
}

func (f *flattener) walk(path string, v any) error {
	switch t := v.(type) {
	case map[string]any:
		if len(t) == 0 && path != "" {
			return f.set(path, "e", "{}")
		}
		for k, c := range t {
			for _, r := range k {
				if !unicode.IsPrint(r) {
					return errBadKey
				}
			}
			p := k
			if path != "" {
				p = path + "." + k
			}
			if err := f.walk(p, c); err != nil {
				return err
			}
		}
	case []any:
		if len(t) == 0 {
			return f.set(path, "e", "[]")
		}
		for i, c := range t {
			if err := f.walk(path+"["+strconv.Itoa(i)+"]", c); err != nil {
				return err
			}
		}
	case string:
		return f.set(path, "s", t)
	case json.Number:
		return f.set(path, "n", t.String())
	case bool:
		return f.set(path, "b", strconv.FormatBool(t))
	case nil:
		return f.set(path, "z", "")
	}
	return nil
}
