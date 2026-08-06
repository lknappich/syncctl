package sshexec

import (
	"strings"
	"testing"
)

// A parsed endpoint becomes an ssh destination, an rsync target, and an
// ssh:// URL. Anything Parse accepts must be free of characters that
// reinterpret any of the three.
func FuzzParseEndpoint(f *testing.F) {
	for _, seed := range []string{
		"host", "host:22", "user@host", "user@host:22", "[::1]:22", "::1",
		"", ":", "@", "host:", ":22", "host:0", "host:99999", "a b",
		"host/path", "host:22:33", "[::1", "user@@host", "-oProxyCommand=x",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		ep, err := ParseEndpoint(raw) // must not panic
		if err != nil {
			return
		}
		for _, field := range []string{ep.User, ep.Host, ep.Port} {
			if strings.ContainsAny(field, " \t\n\r\x00'\"`$&|;<>()\\/") {
				t.Errorf("ParseEndpoint(%q) accepted a metacharacter in %q", raw, field)
			}
		}
		if ep.Host == "" {
			t.Errorf("ParseEndpoint(%q) succeeded with an empty host", raw)
		}
		// A leading dash would be read by ssh as a flag, not a host.
		if strings.HasPrefix(ep.Destination(), "-") {
			t.Errorf("ParseEndpoint(%q) yields destination %q, which ssh reads as a flag", raw, ep.Destination())
		}
	})
}
