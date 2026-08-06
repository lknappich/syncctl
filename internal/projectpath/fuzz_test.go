package projectpath

import (
	"path/filepath"
	"strings"
	"testing"
)

// The security property this package exists for: a path it accepts must
// stay under the repository root once joined. filepath.Join *cleans*
// traversal rather than rejecting it, so acceptance is the only gate.
func FuzzValidateAcceptsNothingThatEscapes(f *testing.F) {
	for _, seed := range []string{
		"group/project", "a/b/c", "../etc", "a/../../b", "/abs", "a//b",
		".", "..", "a/./b", "a\\b", "a\x00b", "", "-", "a.git",
		"....//....//etc", "a/..", "..%2f..", "../x",
	} {
		f.Add(seed)
	}

	const root = "/var/opt/gitlab/git-data/repositories"
	f.Fuzz(func(t *testing.T, path string) {
		if Validate(path) != nil {
			return // rejected; nothing to prove
		}
		joined := filepath.Join(root, path)
		if joined != root && !strings.HasPrefix(joined, root+"/") {
			t.Errorf("Validate accepted %q, which resolves to %q — outside %q", path, joined, root)
		}
		// An accepted path also reaches an ssh:// URL and a shell-free
		// argv, so it must carry nothing that reinterprets either.
		if strings.ContainsAny(path, " \t\n\r\x00\\'\"`$&|;<>()") {
			t.Errorf("Validate accepted %q containing a metacharacter", path)
		}
	})
}
