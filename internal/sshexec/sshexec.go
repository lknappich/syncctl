// Package sshexec centralizes SSH command construction so that host-key
// checking policy is enforced uniformly across all call sites. Every
// SSH invocation in the codebase should go through this package rather
// than building exec.CommandContext("ssh", ...) inline.
package sshexec

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
)

// Runner is the minimal interface for executing a remote command over
// SSH. Both *Config and any test mock satisfy it. Functions that need
// to invoke SSH should accept a Runner rather than a concrete Config
// so tests can inject a mock without touching the network.
type Runner interface {
	CombinedOutput(ctx context.Context, host, remoteCmd string) ([]byte, error)
}

// Endpoint is a parsed ssh_host value. Config carries the destination as
// a single "[user@]host[:port]" string, but no transport accepts it in
// that form: ssh needs the port as a separate -p flag, rsync needs
// "host:path" with the port folded into its -e string, and git needs an
// ssh:// URL. Parsing once here keeps those renderings consistent.
type Endpoint struct {
	User string
	Host string
	Port string
}

// ParseEndpoint splits "[user@]host[:port]" into its parts. Bare IPv6
// literals are accepted unbracketed ("::1"); to give an IPv6 address a
// port, bracket it ("[::1]:22").
func ParseEndpoint(raw string) (Endpoint, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Endpoint{}, fmt.Errorf("ssh_host is empty")
	}
	if strings.ContainsAny(s, " \t\n\r/\x00") {
		return Endpoint{}, fmt.Errorf("ssh_host %q contains invalid characters", raw)
	}

	var ep Endpoint
	if i := strings.LastIndex(s, "@"); i >= 0 {
		ep.User = s[:i]
		s = s[i+1:]
		if ep.User == "" {
			return Endpoint{}, fmt.Errorf("ssh_host %q has an empty user", raw)
		}
	}

	host, port, err := splitHostPort(s)
	if err != nil {
		return Endpoint{}, fmt.Errorf("ssh_host %q: %w", raw, err)
	}
	if host == "" {
		return Endpoint{}, fmt.Errorf("ssh_host %q has an empty host", raw)
	}
	ep.Host, ep.Port = host, port
	return ep, nil
}

// splitHostPort separates an optional :port suffix. An unbracketed value
// with more than one colon is an IPv6 literal, not host:port.
func splitHostPort(s string) (host, port string, err error) {
	if strings.HasPrefix(s, "[") {
		end := strings.Index(s, "]")
		if end < 0 {
			return "", "", fmt.Errorf("unterminated IPv6 literal")
		}
		host = s[1:end]
		rest := s[end+1:]
		switch {
		case rest == "":
			return host, "", nil
		case strings.HasPrefix(rest, ":"):
			port, err = validatePort(rest[1:])
			return host, port, err
		default:
			return "", "", fmt.Errorf("unexpected %q after IPv6 literal", rest)
		}
	}
	switch strings.Count(s, ":") {
	case 0:
		return s, "", nil
	case 1:
		i := strings.Index(s, ":")
		port, err = validatePort(s[i+1:])
		return s[:i], port, err
	default:
		return s, "", nil
	}
}

func validatePort(p string) (string, error) {
	n, err := strconv.Atoi(p)
	if err != nil {
		return "", fmt.Errorf("port %q is not a number", p)
	}
	if n < 1 || n > 65535 {
		return "", fmt.Errorf("port %d out of range 1-65535", n)
	}
	return p, nil
}

// Destination renders the endpoint as ssh and rsync expect it on the
// command line: "[user@]host", with the port carried separately.
func (e Endpoint) Destination() string {
	if e.User != "" {
		return e.User + "@" + e.Host
	}
	return e.Host
}

// GitURL renders an ssh:// URL for the given absolute remote path. This
// is the one form that does take an inline port.
func (e Endpoint) GitURL(path string) string {
	host := e.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if e.Port != "" {
		host += ":" + e.Port
	}
	if e.User != "" {
		host = e.User + "@" + host
	}
	return "ssh://" + host + "/" + strings.TrimPrefix(path, "/")
}

// Config controls SSH options applied to every connection.
type Config struct {
	// KnownHostsFile is the path to a known_hosts file. When set,
	// -o UserKnownHostsFile=<path> is passed and
	// StrictHostKeyChecking defaults to "yes". When empty,
	// StrictHostKeyChecking defaults to "accept-new" (TOFU).
	KnownHostsFile string

	// StrictHostKeyChecking overrides the default. Valid values:
	// "yes", "no", "accept-new". When empty, defaults to "yes" if
	// KnownHostsFile is set, otherwise "accept-new".
	StrictHostKeyChecking string
}

// EffectiveStrictHostKeyChecking returns the resolved checking mode.
func (c Config) EffectiveStrictHostKeyChecking() string {
	if c.StrictHostKeyChecking != "" {
		return c.StrictHostKeyChecking
	}
	if c.KnownHostsFile != "" {
		return "yes"
	}
	return "accept-new"
}

// ExtraArgs returns the host-independent -o options to inject into an
// ssh command.
func (c Config) ExtraArgs() []string {
	args := []string{
		"-o", "StrictHostKeyChecking=" + c.EffectiveStrictHostKeyChecking(),
	}
	if c.KnownHostsFile != "" {
		args = append(args, "-o", "UserKnownHostsFile="+c.KnownHostsFile)
	}
	return args
}

// argsFor returns ExtraArgs plus the endpoint's -p port, if any.
func (c Config) argsFor(ep Endpoint) []string {
	args := c.ExtraArgs()
	if ep.Port != "" {
		args = append(args, "-p", ep.Port)
	}
	return args
}

// Command builds an exec.Cmd for an SSH session to host running remoteCmd.
// The caller is responsible for setting stdout/stderr and calling Run()
// or CombinedOutput().
func (c Config) Command(ctx context.Context, host, remoteCmd string) (*exec.Cmd, error) {
	ep, err := ParseEndpoint(host)
	if err != nil {
		return nil, err
	}
	args := append(c.argsFor(ep), ep.Destination(), remoteCmd)
	// #nosec G204 -- argv vector, not a shell string. remoteCmd is parsed
	// by the *remote* shell; its interpolated values are quoted by
	// internal/shellquote at the call sites that build it.
	return exec.CommandContext(ctx, "ssh", args...), nil
}

// CombinedOutput runs the SSH command and returns combined output.
func (c Config) CombinedOutput(ctx context.Context, host, remoteCmd string) ([]byte, error) {
	cmd, err := c.Command(ctx, host, remoteCmd)
	if err != nil {
		return nil, err
	}
	return cmd.CombinedOutput()
}

// CombinedOutputStdin is CombinedOutput with data piped to the remote
// command's stdin. It exists so secrets can reach a remote process
// without appearing in its argv, which is world-readable through
// /proc/<pid>/cmdline and ps on the remote host.
func (c Config) CombinedOutputStdin(ctx context.Context, host, remoteCmd string, stdin io.Reader) ([]byte, error) {
	cmd, err := c.Command(ctx, host, remoteCmd)
	if err != nil {
		return nil, err
	}
	cmd.Stdin = stdin
	return cmd.CombinedOutput()
}

// SSHString builds the ssh command string for use in rsync's -e flag and
// git's GIT_SSH_COMMAND, e.g.
// "ssh -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/path -p 2222".
// The host is needed because the port lives in this string rather than in
// the rsync target.
func (c Config) SSHString(host string) (string, error) {
	ep, err := ParseEndpoint(host)
	if err != nil {
		return "", err
	}
	return "ssh " + strings.Join(c.argsFor(ep), " "), nil
}

// Default is the fallback Config used when no site-level SSH config is
// provided. It uses accept-new (TOFU) so out-of-the-box behavior matches
// the previous code, but operators can pin host keys via config.
var Default = Config{}

// CheckHost returns an error if host is empty or malformed.
func CheckHost(host string) error {
	if host == "" {
		return fmt.Errorf("ssh_host not configured")
	}
	_, err := ParseEndpoint(host)
	return err
}
