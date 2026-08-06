package shellquote

import (
	"os/exec"
	"strings"
	"testing"
)

// The security property: whatever goes into Quote must come back out of
// a real shell byte-for-byte, as exactly one argument. If that ever fails
// the value has been reinterpreted, which is the injection this package
// exists to prevent.
func FuzzQuoteRoundTripsThroughSh(f *testing.F) {
	for _, seed := range []string{
		"", "/var/opt/gitlab", "a b", "it's", "$HOME", "`id`", "a|b&c;d",
		`back\slash`, "*", "~root", "--flag", "a\nb", "'", `"`, "\t",
		"$(whoami)", "${IFS}", "!!", "a\\'b", "€", "\x01",
	} {
		f.Add(seed)
	}
	if _, err := exec.LookPath("sh"); err != nil {
		f.Skip("sh not available")
	}

	f.Fuzz(func(t *testing.T, in string) {
		// A NUL cannot survive an argv round trip through exec.
		if strings.ContainsRune(in, 0) {
			t.Skip()
		}
		out, err := exec.Command("sh", "-c", "printf %s "+Quote(in)).Output()
		if err != nil {
			t.Fatalf("sh rejected the quoting of %q: %v", in, err)
		}
		if string(out) != in {
			t.Errorf("round trip changed the value:\n in:  %q\n out: %q\n quoted: %s", in, out, Quote(in))
		}
	})
}

// Quoting must always yield a single argument, never several.
func FuzzQuoteYieldsOneArgument(f *testing.F) {
	for _, seed := range []string{"a b", "a  b", "a\tb", "*", "a;b", ""} {
		f.Add(seed)
	}
	if _, err := exec.LookPath("sh"); err != nil {
		f.Skip("sh not available")
	}

	f.Fuzz(func(t *testing.T, in string) {
		if strings.ContainsRune(in, 0) {
			t.Skip()
		}
		out, err := exec.Command("sh", "-c", "set -- "+Quote(in)+"; echo $#").Output()
		if err != nil {
			t.Fatalf("sh rejected %q: %v", in, err)
		}
		if got := strings.TrimSpace(string(out)); got != "1" {
			t.Errorf("Quote(%q) expanded to %s arguments, want 1", in, got)
		}
	})
}
