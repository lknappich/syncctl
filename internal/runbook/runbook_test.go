package runbook

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/lknappich/syncctl/internal/config"
)

func TestGenerateBasic(t *testing.T) {
	cfg := &config.Config{
		Primary: config.SiteConfig{
			Name:        "primary-eu",
			ExternalURL: "https://gitlab.primary.example.com",
			SSHHost:     "primary.example.com:22",
		},
		Secondaries: []config.SiteConfig{
			{Name: "secondary-us", ExternalURL: "https://gitlab.secondary.example.com"},
		},
		Sync: config.SyncConfig{
			SweepInterval:        300000000000,
			LagWarningThreshold:  30000000000,
			LagCriticalThreshold: 300000000000,
			FailoverEnabled:      false,
			ConsistencySamplePct: 0.01,
		},
		Metrics: config.MetricsConfig{Addr: ":9101"},
	}
	var buf bytes.Buffer
	if err := Generate(&buf, cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := buf.String()
	if !contains(out, "primary-eu") {
		t.Error("runbook missing primary name")
	}
	if !contains(out, "secondary-us") {
		t.Error("runbook missing secondary name")
	}
	if !contains(out, "Failover") {
		t.Error("runbook missing failover section")
	}
}

func TestGenerateWithFailoverConfig(t *testing.T) {
	cfg := &config.Config{
		Primary: config.SiteConfig{
			Name:        "p",
			ExternalURL: "https://p.example.com",
		},
		Secondaries: []config.SiteConfig{
			{Name: "s", ExternalURL: "https://s.example.com"},
		},
		Sync: config.SyncConfig{ConsistencySamplePct: 0.05},
		Failover: &config.FailoverConfig{
			AutoFailover:        true,
			QuorumRequired:      2,
			DNSPlugin:           "route53",
			HealthCheckInterval: 15000000000,
		},
	}
	var buf bytes.Buffer
	if err := Generate(&buf, cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := buf.String()
	if !contains(out, "route53") {
		t.Error("runbook missing DNS plugin")
	}
	if !contains(out, "true") {
		t.Error("runbook missing auto-failover value")
	}
}

func contains(s, substr string) bool {
	return bytes.Contains([]byte(s), []byte(substr))
}

func TestGenerateNoSecondaries(t *testing.T) {
	cfg := &config.Config{
		Primary: config.SiteConfig{Name: "p", ExternalURL: "https://p.example.com"},
		Sync:    config.SyncConfig{ConsistencySamplePct: 0.01},
		Metrics: config.MetricsConfig{Addr: ":9101"},
	}
	var buf bytes.Buffer
	if err := Generate(&buf, cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := buf.String()
	if !contains(out, "p") {
		t.Error("runbook missing primary name")
	}
}

func TestGenerateMultipleSecondaries(t *testing.T) {
	cfg := &config.Config{
		Primary: config.SiteConfig{Name: "p", ExternalURL: "https://p.example.com"},
		Secondaries: []config.SiteConfig{
			{Name: "s1", ExternalURL: "https://s1.example.com"},
			{Name: "s2", ExternalURL: "https://s2.example.com"},
			{Name: "s3", ExternalURL: "https://s3.example.com"},
		},
		Sync:    config.SyncConfig{ConsistencySamplePct: 0.01},
		Metrics: config.MetricsConfig{Addr: ":9101"},
	}
	var buf bytes.Buffer
	if err := Generate(&buf, cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := buf.String()
	if !contains(out, "s1") || !contains(out, "s2") || !contains(out, "s3") {
		t.Error("runbook missing secondary names")
	}
}

func TestGenerateWithFailoverDisabled(t *testing.T) {
	cfg := &config.Config{
		Primary: config.SiteConfig{Name: "p", ExternalURL: "https://p.example.com"},
		Secondaries: []config.SiteConfig{
			{Name: "s", ExternalURL: "https://s.example.com"},
		},
		Sync:    config.SyncConfig{FailoverEnabled: false, ConsistencySamplePct: 0.01},
		Metrics: config.MetricsConfig{Addr: ":9101"},
	}
	var buf bytes.Buffer
	if err := Generate(&buf, cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := buf.String()
	if !contains(out, "false") {
		t.Error("runbook should show failover disabled")
	}
}

// The runbook is followed during an incident, so every command it prints
// must be one that actually runs. `syncctl failover` without --yes is
// rejected by the command itself.
func TestGeneratedFailoverCommandCarriesYes(t *testing.T) {
	cfg := &config.Config{
		Primary:     config.SiteConfig{Name: "p", ExternalURL: "https://p.example.com", SSHHost: "p:22"},
		Secondaries: []config.SiteConfig{{Name: "secondary-us"}},
		Sync:        config.SyncConfig{SweepInterval: time.Minute},
	}
	var buf bytes.Buffer
	if err := Generate(&buf, cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "syncctl failover --secondary secondary-us --yes") {
		t.Errorf("runbook must print a runnable failover command, got:\n%s", out)
	}
	if !strings.Contains(out, "--wipe-pgdata") {
		t.Error("runbook should tell the operator how to clear the old primary's PGDATA")
	}
}
func TestRunbookEmitsNewPrimaryFlagWhenAmbiguous(t *testing.T) {
	multi := &config.Config{
		Primary:     config.SiteConfig{Name: "p", ExternalURL: "https://p.example.com", SSHHost: "p:22"},
		Secondaries: []config.SiteConfig{{Name: "secondary-us"}, {Name: "secondary-eu"}},
		Sync:        config.SyncConfig{SweepInterval: time.Minute},
	}
	var buf bytes.Buffer
	if err := Generate(&buf, multi); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(buf.String(), "--new-primary") {
		t.Errorf("runbook must print --new-primary with several secondaries:\n%s", buf.String())
	}

	single := &config.Config{
		Primary:     config.SiteConfig{Name: "p", ExternalURL: "https://p.example.com", SSHHost: "p:22"},
		Secondaries: []config.SiteConfig{{Name: "only"}},
		Sync:        config.SyncConfig{SweepInterval: time.Minute},
	}
	buf.Reset()
	if err := Generate(&buf, single); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if strings.Contains(buf.String(), "--new-primary") {
		t.Error("with one secondary the flag is noise; it should be omitted")
	}
}

// With several secondaries the runbook used to print
// `failover --secondary <first configured> --yes`, choosing the promotion
// target by config order, and promised that auto-failover would promote
// "the first secondary" although the controller refuses to.
func TestRunbookDoesNotChooseAFailoverTargetByConfigOrder(t *testing.T) {
	cfg := &config.Config{
		Primary: config.SiteConfig{Name: "p", ExternalURL: "https://p.example.com", SSHHost: "p:22"},
		Secondaries: []config.SiteConfig{
			{Name: "s1"}, {Name: "s2"}, {Name: "s3"},
		},
	}
	var buf bytes.Buffer
	if err := Generate(&buf, cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "--secondary s1") {
		t.Errorf("runbook picks the first configured secondary as the failover target:\n%s", out)
	}
	for _, want := range []string{
		"    syncctl failover --secondary <name> --yes\n",
		"one of: s1, s2, s3.",
		"promotes nothing: `failover.promote_secondary` is unset",
		"6. Each secondary you did not promote (of s1, s2, s3) still\n   replicates from p",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("runbook missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "first secondary") {
		t.Errorf("runbook claims auto-failover promotes the first secondary:\n%s", out)
	}

	cfg.Failover = &config.FailoverConfig{PromoteSecondary: "s2"}
	buf.Reset()
	if err := Generate(&buf, cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.Contains(buf.String(), "the failover controller promotes s2.") {
		t.Errorf("runbook should name failover.promote_secondary as the auto-failover target:\n%s", buf.String())
	}
}

func TestRunbookWithOneSecondaryHasNothingElseToRepoint(t *testing.T) {
	cfg := &config.Config{
		Primary:     config.SiteConfig{Name: "p", ExternalURL: "https://p.example.com", SSHHost: "p:22"},
		Secondaries: []config.SiteConfig{{Name: "only"}},
	}
	var buf bytes.Buffer
	if err := Generate(&buf, cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "the failover controller promotes only.") {
		t.Errorf("runbook should name the single secondary as the auto-failover target:\n%s", out)
	}
	if strings.Contains(out, "did not promote") {
		t.Errorf("with one secondary there is nothing left to re-point:\n%s", out)
	}
}

// adopt-as-secondary needs --old-primary-ssh when primary.ssh_host is
// unset. Printed with an empty value, the flag swallowed the next token
// (`--new-primary`) as its argument.
func TestRunbookAdoptCommandWithoutPrimarySSHHost(t *testing.T) {
	cfg := &config.Config{
		Primary:     config.SiteConfig{Name: "p", ExternalURL: "https://p.example.com"},
		Secondaries: []config.SiteConfig{{Name: "s1"}, {Name: "s2"}},
	}
	var buf bytes.Buffer
	if err := Generate(&buf, cfg); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	want := "syncctl adopt-as-secondary --old-primary-ssh <old-primary-host:port> --new-primary <promoted-secondary>\n"
	if !strings.Contains(buf.String(), want) {
		t.Errorf("runbook missing %q:\n%s", want, buf.String())
	}
}
