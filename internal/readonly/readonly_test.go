package readonly

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
)

func TestEnableDryRun(t *testing.T) {
	err := Enable(context.Background(), "example.com:22", true)
	if err != nil {
		t.Fatalf("dry-run Enable should not error: %v", err)
	}
}

func TestDisableDryRun(t *testing.T) {
	err := Disable(context.Background(), "example.com:22", true)
	if err != nil {
		t.Fatalf("dry-run Disable should not error: %v", err)
	}
}

func TestEnableEmptySSHHost(t *testing.T) {
	err := Enable(context.Background(), "", true)
	if err == nil {
		t.Fatal("expected error for empty ssh_host")
	}
}

func TestDisableEmptySSHHost(t *testing.T) {
	err := Disable(context.Background(), "", true)
	if err == nil {
		t.Fatal("expected error for empty ssh_host")
	}
}

// The secondary's Postgres is a physical hot standby, so any Rails write
// there fails; a command that discards its exit status or stderr would
// turn that guaranteed failure into a silent no-op.
func TestCommandsDoNotWriteToStandby(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"enable", func() error { return Enable(context.Background(), "example.com:22", true) }},
		{"disable", func() error { return Disable(context.Background(), "example.com:22", true) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmds := dryRunCommands(t, tc.run)
			if len(cmds) == 0 {
				t.Fatal("no commands issued")
			}
			for _, cmd := range cmds {
				for _, bad := range []string{"gitlab-rails", "ApplicationSetting", "|| true", "2>/dev/null"} {
					if strings.Contains(cmd, bad) {
						t.Errorf("command %q contains %q", cmd, bad)
					}
				}
			}
		})
	}
}

func dryRunCommands(t *testing.T, run func() error) []string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	outCh := make(chan []byte)
	go func() {
		b, _ := io.ReadAll(r)
		outCh <- b
	}()

	orig := os.Stdout
	os.Stdout = w
	runErr := func() error {
		defer func() { os.Stdout = orig }()
		return run()
	}()
	w.Close()
	out := <-outCh

	if runErr != nil {
		t.Fatalf("dry run: %v", runErr)
	}
	var cmds []string
	for _, line := range strings.Split(string(out), "\n") {
		if cmd, ok := strings.CutPrefix(line, "[dry-run] ssh "); ok {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}
