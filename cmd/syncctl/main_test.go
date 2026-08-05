package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lknappich/syncctl/internal/config"
)

func TestVersionCmd(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetArgs([]string{"version"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("version: %v", err)
	}
}

func TestRootHelp(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetArgs([]string{"--help"})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("help: %v", err)
	}
	if !strings.Contains(out.String(), "syncctl") {
		t.Errorf("expected 'syncctl' in help: %s", out.String())
	}
	if !strings.Contains(out.String(), "version") {
		t.Errorf("expected 'version' subcommand in help: %s", out.String())
	}
}

func TestConfigValidateCmd(t *testing.T) {
	t.Setenv("PG_REPL_PASSWORD", "replpass")
	t.Setenv("PG_CTRL_PASSWORD", "ctrlpass")
	t.Setenv("S3_AK", "AKIAEXAMPLE")
	t.Setenv("S3_SK", "secretexample")
	t.Setenv("SEC_REPL_PASSWORD", "secpass")

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	yaml := `
primary:
  name: p
  external_url: https://p.example.com
  postgres:
    host: h
    port: 5432
    db: d
    user: u
    password: ${PG_CTRL_PASSWORD}
    replication_user: ru
    replication_password: ${PG_REPL_PASSWORD}
    sslmode: require
  git:
    mode: rsync
    repos_path: /repos
    hashed_storage: true
  object_storage:
    backend: s3
    s3:
      region: us-east-1
      primary_bucket: p
      replica_bucket: r
      access_key: ${S3_AK}
      secret_key: ${S3_SK}
  ssh_host: p:22
secondaries:
  - name: s
    external_url: https://s.example.com
    postgres:
      host: h2
      port: 5432
      db: d
      user: u
      password: ${PG_CTRL_PASSWORD}
      replication_user: ru
      replication_password: ${SEC_REPL_PASSWORD}
      sslmode: require
    git:
      mode: rsync
      repos_path: /repos
      hashed_storage: true
    ssh_host: s:22
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := newRootCmd()
	cmd.SetArgs([]string{"config-validate", "--config", cfgPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("config validate: %v", err)
	}
}

func TestConfigValidateCmdMissingFile(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetArgs([]string{"config-validate", "--config", "/nonexistent/path.yaml"})
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(out)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config file")
	}
}

func TestNewRootCmdHasSubcommands(t *testing.T) {
	cmd := newRootCmd()
	subs := cmd.Commands()
	if len(subs) < 10 {
		t.Errorf("expected at least 10 subcommands, got %d", len(subs))
	}
	expected := []string{"version", "config-validate", "serve", "pg", "sync", "dbkey", "failover", "adopt-as-secondary", "runbook", "sla", "doctor", "init"}
	for _, name := range expected {
		found := false
		for _, sub := range subs {
			if sub.Name() == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing subcommand %q", name)
		}
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	g := &globalFlags{configPath: "/nonexistent.yaml"}
	_, err := loadConfig(g)
	if err == nil {
		t.Fatal("expected error for missing config")
	}
}

func TestFindSecondaryFound(t *testing.T) {
	cfg := &config.Config{
		Secondaries: []config.SiteConfig{
			{Name: "s1"},
			{Name: "s2"},
		},
	}
	got, err := findSecondary(cfg, "s2")
	if err != nil {
		t.Fatalf("findSecondary: %v", err)
	}
	if got.Name != "s2" {
		t.Errorf("Name = %q, want s2", got.Name)
	}
}

func TestFindSecondaryNotFound(t *testing.T) {
	cfg := &config.Config{
		Secondaries: []config.SiteConfig{{Name: "s1"}},
	}
	_, err := findSecondary(cfg, "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown secondary")
	}
}
