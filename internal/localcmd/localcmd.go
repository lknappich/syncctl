// Package localcmd provides a minimal interface for running local
// subprocesses (git, rsync, pg_basebackup, etc.) so that call sites
// can inject mocks in tests without touching the real OS.
package localcmd

import (
	"context"
	"os/exec"
)

// Runner runs a command and returns its combined stdout/stderr output.
// The env slice is appended to os.Environ() before exec.
type Runner interface {
	Run(ctx context.Context, name string, args []string, env []string) ([]byte, error)
}

// Default is the real Runner backed by exec.CommandContext.
var Default Runner = realRunner{}

type realRunner struct{}

func (realRunner) Run(ctx context.Context, name string, args []string, env []string) ([]byte, error) {
	// #nosec G204 -- args are passed as an argv vector, never through a
	// shell, so no quoting or metacharacter handling applies. Values that
	// end up in a *remote* shell go through internal/shellquote instead.
	cmd := exec.CommandContext(ctx, name, args...)
	if len(env) > 0 {
		cmd.Env = append(cmd.Environ(), env...)
	}
	return cmd.CombinedOutput()
}

// RunWith invokes the given runner (falling back to Default if nil) and
// is the convenience call sites use.
func RunWith(ctx context.Context, r Runner, name string, args, env []string) ([]byte, error) {
	if r == nil {
		r = Default
	}
	return r.Run(ctx, name, args, env)
}
