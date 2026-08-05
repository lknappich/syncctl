package version

import (
	"strings"
	"testing"
)

func TestCurrentString(t *testing.T) {
	v := Current()
	s := v.String()
	if !strings.Contains(s, "syncctl") {
		t.Errorf("expected version string to contain 'syncctl', got: %s", s)
	}
}
