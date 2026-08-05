package dbkey

import (
	"context"
	"strings"
	"testing"

	"github.com/lknappich/syncctl/internal/sshexec"
)

func TestDbKeyRegexMatchesQuoted(t *testing.T) {
	line := []byte(`gitlab_rails['db_key_base'] = 'abc123def456`)
	m := dbKeyRe.FindSubmatch(line)
	if m == nil {
		t.Fatal("regex did not match quoted form")
	}
	if string(m[1]) != "abc123def456" {
		t.Errorf("got %q", m[1])
	}
}

func TestDbKeyRegexMatchesUnquoted(t *testing.T) {
	line := []byte(`gitlab_rails['db_key_base'] = abc123def456`)
	m := dbKeyRe.FindSubmatch(line)
	if m == nil {
		t.Fatal("regex did not match unquoted form")
	}
	if string(m[1]) != "abc123def456" {
		t.Errorf("got %q", m[1])
	}
}

func TestDbKeyRegexNoMatch(t *testing.T) {
	line := []byte(`# gitlab_rails['db_key_base'] = 'something'`)
	m := dbKeyRe.FindSubmatch(line)
	if m != nil {
		t.Error("regex should not match commented-out line")
	}
}

func TestDbKeyRegexMatchesLeadingSpaces(t *testing.T) {
	line := []byte(`  gitlab_rails['db_key_base'] = 'mykey123`)
	m := dbKeyRe.FindSubmatch(line)
	if m == nil {
		t.Fatal("regex did not match line with leading spaces")
	}
	if string(m[1]) != "mykey123" {
		t.Errorf("got %q", m[1])
	}
}

func TestFetchKeyEmptySSHHost(t *testing.T) {
	_, err := fetchKey(context.Background(), "", sshexec.Default)
	if err == nil {
		t.Fatal("expected error for empty ssh_host")
	}
}

// mockRunner is a fake sshexec.Runner used in tests.
type mockRunner struct {
	out        []byte
	err        error
	calls      []call
	perHost    map[string][]byte
	perHostErr map[string]error
}

type call struct {
	host string
	cmd  string
}

func (m *mockRunner) CombinedOutput(ctx context.Context, host, cmd string) ([]byte, error) {
	m.calls = append(m.calls, call{host, cmd})
	if m.perHost != nil {
		if out, ok := m.perHost[host]; ok {
			var err error
			if m.perHostErr != nil {
				err = m.perHostErr[host]
			}
			return out, err
		}
	}
	return m.out, m.err
}

func (m *mockRunner) SSHString() string { return "mock-ssh" }

func TestCheckWithRunnerMatch(t *testing.T) {
	m := &mockRunner{perHost: map[string][]byte{
		"primary:22":   []byte("gitlab_rails['db_key_base'] = 'samekey123"),
		"secondary:22": []byte("gitlab_rails['db_key_base'] = 'samekey123"),
	}}
	err := CheckWithRunner(context.Background(), "primary:22", "secondary:22", m)
	if err != nil {
		t.Fatalf("expected match, got: %v", err)
	}
	if len(m.calls) < 2 {
		t.Errorf("expected at least 2 calls, got %d", len(m.calls))
	}
}

func TestCheckWithRunnerMismatch(t *testing.T) {
	m := &mockRunner{perHost: map[string][]byte{
		"p:22": []byte("gitlab_rails['db_key_base'] = 'keyA"),
		"s:22": []byte("gitlab_rails['db_key_base'] = 'keyB"),
	}}
	err := CheckWithRunner(context.Background(), "p:22", "s:22", m)
	if err == nil {
		t.Fatal("expected mismatch error")
	}
}

func TestCheckWithRunnerPrimaryFails(t *testing.T) {
	m := &mockRunner{err: errString("connection refused")}
	err := CheckWithRunner(context.Background(), "p:22", "s:22", m)
	if err == nil {
		t.Fatal("expected error when primary SSH fails")
	}
}

func TestCheckWithRunnerKeyNotFound(t *testing.T) {
	m := &mockRunner{out: []byte("nothing relevant here")}
	err := CheckWithRunner(context.Background(), "p:22", "s:22", m)
	if err == nil {
		t.Fatal("expected error when key not found")
	}
}

func TestCheckWithRunnerEmptyHost(t *testing.T) {
	m := &mockRunner{}
	err := CheckWithRunner(context.Background(), "", "s:22", m)
	if err == nil {
		t.Fatal("expected error for empty host")
	}
	if len(m.calls) != 0 {
		t.Errorf("no SSH calls should be made for empty host, got %d", len(m.calls))
	}
}

func TestFetchKeyYAMLFallback(t *testing.T) {
	m := &mockRunner{out: []byte("db_key_base: yamlkey456")}
	key, err := fetchKey(context.Background(), "h:22", m)
	if err != nil {
		t.Fatalf("fetchKey: %v", err)
	}
	if key != "yamlkey456" {
		t.Errorf("got %q, want yamlkey456", key)
	}
}

func TestTryFetchKeyWithSudoPrependsCommand(t *testing.T) {
	m := &mockRunner{}
	_, _ = tryFetchKey(context.Background(), "h:22", m, true)
	if len(m.calls) != 1 {
		t.Fatalf("calls = %d", len(m.calls))
	}
	if !strings.Contains(m.calls[0].cmd, "sudo grep") {
		t.Errorf("withSudo should prepend sudo grep: %q", m.calls[0].cmd)
	}
}

func TestTryFetchKeyWithoutSudo(t *testing.T) {
	m := &mockRunner{}
	_, _ = tryFetchKey(context.Background(), "h:22", m, false)
	if len(m.calls) != 1 {
		t.Fatalf("calls = %d", len(m.calls))
	}
	if strings.Contains(m.calls[0].cmd, "sudo grep 'db_key_base'") {
		t.Errorf("withoutSudo should not have leading sudo grep: %q", m.calls[0].cmd)
	}
}

func TestCheckWithRunnerFallsBackOnErr(t *testing.T) {
	// First call (with sudo) fails; second call (without sudo) succeeds.
	m := &fallbackRunner{
		errOnCall: 1,
		err:       errString("sudo failed"),
		out:       []byte("gitlab_rails['db_key_base'] = 'key"),
	}
	err := CheckWithRunner(context.Background(), "h:22", "h:22", m)
	if err != nil {
		t.Fatalf("expected fallback to succeed: %v", err)
	}
}

type fallbackRunner struct {
	errOnCall int
	call      int
	err       error
	out       []byte
}

func (m *fallbackRunner) CombinedOutput(ctx context.Context, host, cmd string) ([]byte, error) {
	m.call++
	if m.call == m.errOnCall {
		return nil, m.err
	}
	return m.out, nil
}

func (m *fallbackRunner) SSHString() string { return "mock-ssh" }

type errString string

func (e errString) Error() string { return string(e) }
