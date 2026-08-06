package logging

import (
	"strings"
	"testing"
)

func TestProjectPathRedaction(t *testing.T) {
	t.Cleanup(func() { SetRedactProjectPaths(false) })

	SetRedactProjectPaths(false)
	if got := ProjectPath("acme-corp/secret-product"); got != "acme-corp/secret-product" {
		t.Errorf("off: got %q, want the path unchanged", got)
	}

	SetRedactProjectPaths(true)
	got := ProjectPath("acme-corp/secret-product")
	if strings.Contains(got, "acme-corp") || strings.Contains(got, "secret-product") {
		t.Errorf("redacted form still leaks the name: %q", got)
	}
	if !strings.HasPrefix(got, "redacted:") {
		t.Errorf("got %q, want a redacted: prefix", got)
	}
	// Stable, so failures stay correlatable across log lines.
	if again := ProjectPath("acme-corp/secret-product"); again != got {
		t.Errorf("not stable: %q then %q", got, again)
	}
	// Distinct, so two projects are distinguishable.
	if other := ProjectPath("acme-corp/other-product"); other == got {
		t.Error("different paths collided")
	}
	// Empty stays empty rather than hashing to a constant.
	if ProjectPath("") != "" {
		t.Error("empty path should stay empty")
	}
}

func TestCommandOutputIsBounded(t *testing.T) {
	if got := CommandOutput("  short output  "); got != "short output" {
		t.Errorf("got %q", got)
	}
	long := strings.Repeat("refs/heads/customer-name-branch ", 100)
	got := CommandOutput(long)
	if len(got) >= len(long) {
		t.Errorf("output not truncated: %d chars", len(got))
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("truncation should be visible in the line: %q", got)
	}
	if !strings.Contains(got, "debug") {
		t.Errorf("should point at where the full output is: %q", got)
	}
}
