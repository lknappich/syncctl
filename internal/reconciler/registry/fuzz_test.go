package registry

import (
	"strings"
	"testing"
)

// parseChallenge reads a WWW-Authenticate header straight off the wire
// from a server that may be hostile. It must never panic, and must never
// report ok with an empty realm — the realm is what gets fetched.
func FuzzParseChallenge(f *testing.F) {
	for _, seed := range []string{
		`Bearer realm="https://auth.example.com/token",service="registry",scope="repository:a:pull"`,
		`Bearer realm="https://a/b"`,
		"Bearer", "Basic realm=x", "", `Bearer realm=`, `Bearer realm="`,
		`Bearer realm="a,b",service="c"`, `Bearer realm="a\"b"`,
		`Bearer realm="https://a", realm="https://b"`,
		strings.Repeat(`Bearer realm="x",`, 50),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, header string) {
		c, ok := parseChallenge(header) // must not panic
		if !ok {
			return
		}
		if c.Realm == "" {
			t.Errorf("parseChallenge(%q) reported ok with an empty realm", header)
		}
		if !strings.HasPrefix(header, "Bearer ") {
			t.Errorf("parseChallenge accepted a non-Bearer scheme: %q", header)
		}
	})
}
