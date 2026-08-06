package shellquote

import (
	"os/exec"
	"strings"
	"testing"
)

func TestQuote(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "''"},
		{"plain path", "/var/opt/gitlab/git-data", "/var/opt/gitlab/git-data"},
		{"safe punctuation", "user@host:/a,b+c=d%e", "user@host:/a,b+c=d%e"},
		{"space", "/mnt/git data", "'/mnt/git data'"},
		{"semicolon", "/tmp; rm -rf /", "'/tmp; rm -rf /'"},
		{"backtick", "/tmp/`id`", "'/tmp/`id`'"},
		{"dollar", "/tmp/$HOME", "'/tmp/$HOME'"},
		{"single quote", "it's", `'it'\''s'`},
		{"only quote", "'", `''\'''`},
		{"newline", "a\nb", "'a\nb'"},
		{"pipe", "a|b", "'a|b'"},
		{"ampersand", "a&b", "'a&b'"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Quote(tc.in); got != tc.want {
				t.Errorf("Quote(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A real shell must echo back exactly what went in, for every input the
// table above covers and a few more. This is the property that matters;
// the string comparisons above only pin the encoding.
func TestQuoteRoundTripsThroughSh(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	inputs := []string{
		"/var/opt/gitlab/git-data",
		"/mnt/git data/repositories",
		"it's",
		"a|b&c;d",
		"$HOME",
		"`id`",
		`back\slash`,
		"*",
		"~root",
		"--not-a-flag",
	}
	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			out, err := exec.Command("sh", "-c", "printf %s "+Quote(in)).Output()
			if err != nil {
				t.Fatalf("sh: %v", err)
			}
			if string(out) != in {
				t.Errorf("round trip: got %q, want %q", out, in)
			}
		})
	}
}

// An injection attempt must reach the command as one argument, not as a
// second command.
func TestQuoteBlocksCommandInjection(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	out, err := exec.Command("sh", "-c", "printf %s "+Quote("x; echo PWNED")).Output()
	if err != nil {
		t.Fatalf("sh: %v", err)
	}
	if strings.Contains(string(out), "PWNED\n") {
		t.Errorf("injected command executed: %q", out)
	}
	if string(out) != "x; echo PWNED" {
		t.Errorf("got %q, want the literal argument", out)
	}
}
