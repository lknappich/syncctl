package runbook

import (
	"bytes"
	"testing"

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
