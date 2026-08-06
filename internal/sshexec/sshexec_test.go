package sshexec

import (
	"strings"
	"testing"
)

func TestEffectiveStrictHostKeyChecking(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{"default", Config{}, "accept-new"},
		{"with known_hosts", Config{KnownHostsFile: "/etc/known_hosts"}, "yes"},
		{"explicit override", Config{KnownHostsFile: "/etc/known_hosts", StrictHostKeyChecking: "no"}, "no"},
		{"explicit accept-new", Config{StrictHostKeyChecking: "accept-new"}, "accept-new"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.cfg.EffectiveStrictHostKeyChecking()
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExtraArgsIncludesKnownHostsFile(t *testing.T) {
	cfg := Config{KnownHostsFile: "/etc/known_hosts"}
	args := cfg.ExtraArgs()
	found := false
	for i, a := range args {
		if a == "-o" && i+1 < len(args) && args[i+1] == "UserKnownHostsFile=/etc/known_hosts" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected UserKnownHostsFile in args, got %v", args)
	}
}

func TestExtraArgsDefaultNoKnownHostsFile(t *testing.T) {
	cfg := Config{}
	args := cfg.ExtraArgs()
	for i, a := range args {
		if a == "-o" && i+1 < len(args) && args[i+1] == "StrictHostKeyChecking=accept-new" {
			return
		}
	}
	t.Errorf("expected StrictHostKeyChecking=accept-new in args, got %v", args)
}

func TestSSHString(t *testing.T) {
	cfg := Config{KnownHostsFile: "/etc/known_hosts"}
	s, err := cfg.SSHString("example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s != "ssh -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/etc/known_hosts" {
		t.Errorf("got %q", s)
	}
}

func TestSSHStringCarriesPort(t *testing.T) {
	s, err := Config{}.SSHString("example.com:2222")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s != "ssh -o StrictHostKeyChecking=accept-new -p 2222" {
		t.Errorf("got %q", s)
	}
}

func TestCheckHost(t *testing.T) {
	if err := CheckHost(""); err == nil {
		t.Error("expected error for empty host")
	}
	if err := CheckHost("example.com:22"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := CheckHost("example.com:not-a-port"); err == nil {
		t.Error("expected error for non-numeric port")
	}
}

func TestParseEndpoint(t *testing.T) {
	tests := []struct {
		raw            string
		user           string
		host           string
		port           string
		dest           string
		gitURL         string
		wantParseError bool
	}{
		{raw: "example.com", host: "example.com", dest: "example.com",
			gitURL: "ssh://example.com/srv/repos"},
		{raw: "example.com:22", host: "example.com", port: "22", dest: "example.com",
			gitURL: "ssh://example.com:22/srv/repos"},
		{raw: "git@example.com", user: "git", host: "example.com", dest: "git@example.com",
			gitURL: "ssh://git@example.com/srv/repos"},
		{raw: "git@example.com:2222", user: "git", host: "example.com", port: "2222",
			dest: "git@example.com", gitURL: "ssh://git@example.com:2222/srv/repos"},
		{raw: "10.0.0.10", host: "10.0.0.10", dest: "10.0.0.10",
			gitURL: "ssh://10.0.0.10/srv/repos"},
		{raw: "::1", host: "::1", dest: "::1", gitURL: "ssh://[::1]/srv/repos"},
		{raw: "[::1]:22", host: "::1", port: "22", dest: "::1",
			gitURL: "ssh://[::1]:22/srv/repos"},
		{raw: "  example.com:22  ", host: "example.com", port: "22", dest: "example.com",
			gitURL: "ssh://example.com:22/srv/repos"},
		{raw: "", wantParseError: true},
		{raw: "example.com:0", wantParseError: true},
		{raw: "example.com:99999", wantParseError: true},
		{raw: "example.com:ssh", wantParseError: true},
		{raw: "@example.com", wantParseError: true},
		{raw: "git@", wantParseError: true},
		{raw: "example.com/path", wantParseError: true},
		{raw: "example.com 22", wantParseError: true},
		{raw: "[::1:22", wantParseError: true},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			ep, err := ParseEndpoint(tc.raw)
			if tc.wantParseError {
				if err == nil {
					t.Fatalf("expected error, got %+v", ep)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ep.User != tc.user || ep.Host != tc.host || ep.Port != tc.port {
				t.Errorf("got user=%q host=%q port=%q, want user=%q host=%q port=%q",
					ep.User, ep.Host, ep.Port, tc.user, tc.host, tc.port)
			}
			if got := ep.Destination(); got != tc.dest {
				t.Errorf("Destination() = %q, want %q", got, tc.dest)
			}
			if got := ep.GitURL("/srv/repos"); got != tc.gitURL {
				t.Errorf("GitURL() = %q, want %q", got, tc.gitURL)
			}
		})
	}
}

func TestCommandUsesBareHostAndPortFlag(t *testing.T) {
	cmd, err := Config{}.Command(t.Context(), "git@example.com:2222", "echo ok")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "-p 2222") {
		t.Errorf("expected -p 2222 in %q", args)
	}
	if !strings.Contains(args, "git@example.com echo ok") {
		t.Errorf("expected bare destination in %q", args)
	}
	if strings.Contains(args, "example.com:2222") {
		t.Errorf("port must not stay in the destination: %q", args)
	}
}

// ssh and rsync read a destination beginning with "-" as an option, so
// "-oProxyCommand=..." executes a command instead of connecting. Quoting
// cannot help: the value is an argument, not part of a command string.
func TestParseEndpointRejectsFlagLikeDestinations(t *testing.T) {
	for _, raw := range []string{
		"-oProxyCommand=touch /tmp/pwned",
		"-oProxyCommand=x",
		"-l",
		"-",
		"--",
		"-x@host",
		"-host:22",
	} {
		t.Run(raw, func(t *testing.T) {
			if ep, err := ParseEndpoint(raw); err == nil {
				t.Errorf("accepted %q, destination would be %q", raw, ep.Destination())
			}
		})
	}
}

// The tightened charset must not exclude anything real.
func TestRealWorldHostsStillParse(t *testing.T) {
	for _, raw := range []string{
		"gitlab.example.com", "gitlab.example.com:22", "git@gitlab.example.com",
		"git@gitlab.example.com:2222", "10.0.0.10", "10.0.0.10:22",
		"::1", "[::1]:22", "fe80::1%eth0", "my_host-1.internal.example.com",
		"gitlab-primary.eu-west-1.compute.internal:22",
	} {
		if _, err := ParseEndpoint(raw); err != nil {
			t.Errorf("legitimate host %q rejected: %v", raw, err)
		}
	}
}
