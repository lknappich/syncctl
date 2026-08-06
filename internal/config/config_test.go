package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTempConfig writes a YAML file in a temp dir and returns its path.
func writeTempConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return p
}

func TestLoadMinimal(t *testing.T) {
	// All required env-secret placeholders must be set before Load.
	t.Setenv("PG_REPL_PASSWORD", "replpass")
	t.Setenv("PG_CTRL_PASSWORD", "ctrlpass")
	t.Setenv("S3_AK", "AKIAEXAMPLE")
	t.Setenv("S3_SK", "secretexample")
	t.Setenv("SEC_REPL_PASSWORD", "secpass")

	yaml := `
primary:
  name: primary-eu
  external_url: https://gitlab.primary.example.com
  postgres:
    host: 10.0.0.10
    port: 5432
    db: gitlabhq_production
    user: gitlab
    password: ${PG_CTRL_PASSWORD}
    replication_user: gitlab_repl
    replication_password: ${PG_REPL_PASSWORD}
  git:
    mode: rsync
    repos_path: /var/opt/gitlab/git-data/repositories
  object_storage:
    backend: s3
    s3:
      region: eu-west-1
      primary_bucket: gitlab-primary
      replica_bucket: gitlab-replica
      access_key: ${S3_AK}
      secret_key: ${S3_SK}
secondaries:
  - name: secondary-us
    external_url: https://gitlab.secondary.example.com
    postgres:
      host: 10.1.0.10
      port: 5432
      db: gitlabhq_production
      user: gitlab
      password: ${PG_CTRL_PASSWORD}
      replication_user: gitlab_repl
      replication_password: ${SEC_REPL_PASSWORD}
    git:
      mode: rsync
      repos_path: /var/opt/gitlab/git-data/repositories
    object_storage:
      backend: s3
      s3:
        region: us-east-1
        primary_bucket: gitlab-primary
        replica_bucket: gitlab-replica
        access_key: ${S3_AK}
        secret_key: ${S3_SK}
sync:
  sweep_interval: 1m
  failover_enabled: true
metrics:
  addr: ":9101"
log:
  level: debug
  format: text
control_db: sqlite://data/syncctl.db
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Primary.Name != "primary-eu" {
		t.Errorf("primary name = %q", cfg.Primary.Name)
	}
	if cfg.Primary.Postgres.ReplicationPassword != "replpass" {
		t.Errorf("repl password not expanded: %q", cfg.Primary.Postgres.ReplicationPassword)
	}
	if cfg.Sync.SweepInterval.String() != "1m0s" {
		t.Errorf("sweep interval = %v", cfg.Sync.SweepInterval)
	}
	if len(cfg.Secondaries) != 1 || cfg.Secondaries[0].Name != "secondary-us" {
		t.Errorf("secondaries = %+v", cfg.Secondaries)
	}
}

func TestLoadRejectsMissingEnv(t *testing.T) {
	// Intentionally do NOT set PG_REPL_PASSWORD.
	t.Setenv("PG_CTRL_PASSWORD", "x")

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
    replication_user: r
    replication_password: ${PG_REPL_PASSWORD}
  git:
    mode: rsync
    repos_path: /r
  object_storage:
    backend: fs
secondaries:
  - name: s
    postgres:
      host: h
      port: 5432
      db: d
      user: u
      replication_user: r
      replication_password: ${SEC_REPL_PASSWORD}
    git:
      mode: rsync
      repos_path: /r
    object_storage:
      backend: fs
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing env vars, got nil")
	}
}

func TestValidateRejectsUnknownGitMode(t *testing.T) {
	t.Setenv("PG_REPL_PASSWORD", "x")
	t.Setenv("PG_CTRL_PASSWORD", "x")
	t.Setenv("SEC_REPL_PASSWORD", "x")

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
    replication_user: r
    replication_password: ${PG_REPL_PASSWORD}
  git:
    mode: teleport
  object_storage:
    backend: fs
secondaries:
  - name: s
    postgres:
      host: h
      port: 5432
      db: d
      user: u
      replication_user: r
      replication_password: ${SEC_REPL_PASSWORD}
    git:
      mode: rsync
      repos_path: /r
    object_storage:
      backend: fs
`
	path := writeTempConfig(t, yaml)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid git mode, got nil")
	}
}

func TestDSNDefaultSSLModeIsRequire(t *testing.T) {
	pg := PostgresConfig{
		Host:     "db.example.com",
		Port:     5432,
		DB:       "gitlab",
		User:     "gitlab",
		Password: "secret",
	}
	dsn := pg.DSN()
	if !strings.Contains(dsn, "sslmode=require") {
		t.Errorf("expected sslmode=require in DSN, got: %s", dsn)
	}
	if strings.Contains(dsn, "sslmode=disable") {
		t.Errorf("DSN should not contain sslmode=disable by default, got: %s", dsn)
	}
}

func TestDSNExplicitDisable(t *testing.T) {
	pg := PostgresConfig{
		Host:     "localhost",
		Port:     5432,
		DB:       "gitlab",
		User:     "gitlab",
		Password: "secret",
		SSLMode:  "disable",
	}
	dsn := pg.DSN()
	if !strings.Contains(dsn, "sslmode=disable") {
		t.Errorf("expected sslmode=disable when explicitly set, got: %s", dsn)
	}
}

func TestDSNPasswordSpecialChars(t *testing.T) {
	tests := []struct {
		name string
		pw   string
	}{
		{"simple", "p@ssw0rd"},
		{"with space", "p@ss word"},
		{"with single quote", "p@ss'word"},
		{"with backslash", `p@ss\word`},
		{"with space quote backslash", `p@'s w\rd`},
		{"empty", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pg := PostgresConfig{
				Host:     "db.example.com",
				Port:     5432,
				DB:       "gitlab",
				User:     "gitlab",
				Password: tc.pw,
			}
			dsn := pg.DSN()
			if !strings.Contains(dsn, "password=") {
				t.Fatalf("DSN missing password field: %s", dsn)
			}
			parsed := parseDSNPassword(t, dsn)
			if parsed != tc.pw {
				t.Errorf("password round-trip failed: got %q, want %q (dsn=%s)", parsed, tc.pw, dsn)
			}
		})
	}
}

func TestReplicationDSNContainsAppName(t *testing.T) {
	pg := PostgresConfig{
		Host:                "db.example.com",
		Port:                5432,
		ReplicationUser:     "repl",
		ReplicationPassword: "secret",
	}
	dsn := pg.ReplicationDSN()
	if !strings.Contains(dsn, "application_name=syncctl") {
		t.Errorf("expected application_name in replication DSN, got: %s", dsn)
	}
	if !strings.Contains(dsn, "sslmode=require") {
		t.Errorf("expected sslmode=require in replication DSN, got: %s", dsn)
	}
}

func TestDSNSSLCertPaths(t *testing.T) {
	pg := PostgresConfig{
		Host:        "db.example.com",
		Port:        5432,
		DB:          "gitlab",
		User:        "gitlab",
		Password:    "secret",
		SSLMode:     "verify-full",
		SSLRootCert: "/etc/ssl/certs/ca.pem",
		SSLCert:     "/etc/ssl/client.pem",
		SSLKey:      "/etc/ssl/client.key",
	}
	dsn := pg.DSN()
	for _, want := range []string{
		"sslmode=verify-full",
		"sslrootcert=/etc/ssl/certs/ca.pem",
		"sslcert=/etc/ssl/client.pem",
		"sslkey=/etc/ssl/client.key",
	} {
		if !strings.Contains(dsn, want) {
			t.Errorf("expected %q in DSN, got: %s", want, dsn)
		}
	}
}

func TestEnvExpansionDoesNotInjectYAMLKeys(t *testing.T) {
	t.Setenv("PG_REPL_PASSWORD", "replpass")
	t.Setenv("PG_CTRL_PASSWORD", "ctrlpass")
	t.Setenv("SEC_REPL_PASSWORD", "secpass")
	t.Setenv("S3_AK", "AKIAEXAMPLE")
	t.Setenv("S3_SK", "secretexample")
	t.Setenv("MALICIOUS_VALUE", "foo\npassword: attacker_key")

	yaml := `
primary:
  name: p
  external_url: https://p.example.com
  postgres:
    host: h
    port: 5432
    db: d
    user: u
    password: ${MALICIOUS_VALUE}
    replication_user: r
    replication_password: ${PG_REPL_PASSWORD}
  git:
    mode: rsync
    repos_path: /r
  object_storage:
    backend: fs
secondaries:
  - name: s
    postgres:
      host: h
      port: 5432
      db: d
      user: u
      replication_user: r
      replication_password: ${SEC_REPL_PASSWORD}
    git:
      mode: rsync
      repos_path: /r
    object_storage:
      backend: fs
`
	path := writeTempConfig(t, yaml)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Primary.Postgres.Password != "foo\npassword: attacker_key" {
		t.Errorf("expected multi-line value to be opaque string, got %q", cfg.Primary.Postgres.Password)
	}
	if cfg.Primary.Postgres.ReplicationPassword != "replpass" {
		t.Errorf("expected replpass, got %q", cfg.Primary.Postgres.ReplicationPassword)
	}
}

func TestQuoteLibPQValue(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"simple", "simple"},
		{"", "''"},
		{"has space", "'has space'"},
		{"has'quote", "'has\\'quote'"},
		{`has\backslash`, `'has\\backslash'`},
		{"no-special-chars-123", "no-special-chars-123"},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got := quoteLibPQValue(tc.in)
			if got != tc.want {
				t.Errorf("quoteLibPQValue(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func parseDSNPassword(t *testing.T, dsn string) string {
	t.Helper()
	idx := strings.Index(dsn, "password=")
	if idx < 0 {
		t.Fatalf("no password= field in DSN: %s", dsn)
	}
	v := dsn[idx+len("password="):]
	if v == "''" {
		return ""
	}
	if !strings.HasPrefix(v, "'") {
		spaceIdx := strings.IndexByte(v, ' ')
		if spaceIdx < 0 {
			return v
		}
		return v[:spaceIdx]
	}
	inner := v[1:]
	var sb strings.Builder
	for i := 0; i < len(inner); i++ {
		if inner[i] == '\\' && i+1 < len(inner) {
			if inner[i+1] == '\\' {
				sb.WriteByte('\\')
				i++
			} else if inner[i+1] == '\'' {
				sb.WriteByte('\'')
				i++
			}
			continue
		}
		if inner[i] == '\'' {
			break
		}
		sb.WriteByte(inner[i])
	}
	return sb.String()
}

func TestExpandEnvReplacesAllRefs(t *testing.T) {
	t.Setenv("FOO", "bar")
	t.Setenv("BAZ", "qux")
	in := []byte("key: ${FOO}\nother: ${BAZ}")
	out, err := ExpandEnv(in)
	if err != nil {
		t.Fatalf("ExpandEnv: %v", err)
	}
	want := "key: bar\nother: qux"
	if string(out) != want {
		t.Errorf("got %q, want %q", out, want)
	}
}

func TestExpandEnvMissingVar(t *testing.T) {
	os.Unsetenv("DEFINITELY_UNSET_VAR_XYZ")
	in := []byte("key: ${DEFINITELY_UNSET_VAR_XYZ}")
	_, err := ExpandEnv(in)
	if err == nil {
		t.Fatal("expected error for missing env var")
	}
	if !strings.Contains(err.Error(), "DEFINITELY_UNSET_VAR_XYZ") {
		t.Errorf("error should name the missing var: %v", err)
	}
}

func TestExpandEnvEmptyVarIsMissing(t *testing.T) {
	t.Setenv("EMPTY_VAR", "")
	in := []byte("key: ${EMPTY_VAR}")
	_, err := ExpandEnv(in)
	if err == nil {
		t.Fatal("empty env var should be treated as missing")
	}
}

func TestExpandEnvNoRefs(t *testing.T) {
	in := []byte("key: plain-value\n")
	out, err := ExpandEnv(in)
	if err != nil {
		t.Fatalf("ExpandEnv: %v", err)
	}
	if string(out) != string(in) {
		t.Errorf("got %q, want %q", out, in)
	}
}

func TestSSHExecConfig(t *testing.T) {
	c := &Config{SSH: SSHConfig{KnownHostsFile: "/etc/ssh/known_hosts", StrictHostKeyChecking: "yes"}}
	got := c.SSHExecConfig()
	if got.KnownHostsFile != "/etc/ssh/known_hosts" {
		t.Errorf("KnownHostsFile = %q", got.KnownHostsFile)
	}
	if got.StrictHostKeyChecking != "yes" {
		t.Errorf("StrictHostKeyChecking = %q", got.StrictHostKeyChecking)
	}
}

func TestSSHExecConfigDefaults(t *testing.T) {
	c := &Config{}
	got := c.SSHExecConfig()
	if got.KnownHostsFile != "" {
		t.Errorf("KnownHostsFile = %q, want empty", got.KnownHostsFile)
	}
	if got.StrictHostKeyChecking != "" {
		t.Errorf("StrictHostKeyChecking = %q, want empty", got.StrictHostKeyChecking)
	}
}

func TestResolveEnvRejectsMissing(t *testing.T) {
	os.Unsetenv("MISSING_PG_PWD")
	c := &Config{Primary: SiteConfig{Postgres: PostgresConfig{Password: "${MISSING_PG_PWD}"}}}
	err := resolveEnvInStruct(c)
	if err == nil {
		t.Fatal("expected error for missing env var")
	}
}

func TestResolveEnvHandlesNilPointers(t *testing.T) {
	c := &Config{Failover: nil}
	err := resolveEnvInStruct(c)
	if err != nil {
		t.Fatalf("resolveEnvInStruct with nil pointer: %v", err)
	}
}

func TestResolveEnvExpandsSlice(t *testing.T) {
	t.Setenv("FIRST_SEC", "s1")
	c := &Config{Secondaries: []SiteConfig{{Name: "${FIRST_SEC}"}}}
	err := resolveEnvInStruct(c)
	if err != nil {
		t.Fatalf("resolveEnvInStruct slice: %v", err)
	}
	if c.Secondaries[0].Name != "s1" {
		t.Errorf("Name = %q, want s1", c.Secondaries[0].Name)
	}
}

func TestValidateSSHHosts(t *testing.T) {
	tests := []struct {
		name      string
		primary   string
		secondary string
		wantErr   bool
	}{
		{name: "both empty"},
		{name: "host only", primary: "p.example.com", secondary: "s.example.com"},
		{name: "host and port", primary: "p.example.com:22", secondary: "s.example.com:2222"},
		{name: "user and port", primary: "git@p.example.com:22", secondary: "s.example.com"},
		{name: "bracketed ipv6", primary: "[::1]:22", secondary: "s.example.com"},
		{name: "bad primary port", primary: "p.example.com:ssh", wantErr: true},
		{name: "bad secondary port", primary: "p.example.com", secondary: "s:0", wantErr: true},
		{name: "path in host", primary: "p.example.com/repos", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{
				Primary:     SiteConfig{SSHHost: tc.primary},
				Secondaries: []SiteConfig{{SSHHost: tc.secondary}},
			}
			var errs []error
			c.validateSSHHosts(&errs)
			if tc.wantErr && len(errs) == 0 {
				t.Error("expected a validation error")
			}
			if !tc.wantErr && len(errs) != 0 {
				t.Errorf("unexpected errors: %v", errs)
			}
		})
	}
}

func TestReplicationConnInfoOmitsPassword(t *testing.T) {
	pg := PostgresConfig{
		Host:                "db.example.com",
		Port:                5432,
		ReplicationUser:     "gitlab_repl",
		ReplicationPassword: "s3cr3t",
	}
	dsn, password := pg.ReplicationConnInfo()
	if strings.Contains(dsn, "password") {
		t.Errorf("DSN must carry no password field: %s", dsn)
	}
	if strings.Contains(dsn, "s3cr3t") {
		t.Errorf("password leaked into DSN: %s", dsn)
	}
	if password != "s3cr3t" {
		t.Errorf("password = %q, want s3cr3t", password)
	}
	if !strings.Contains(dsn, "user=gitlab_repl") || !strings.Contains(dsn, "dbname=replication") {
		t.Errorf("DSN lost its connection fields: %s", dsn)
	}
}
func TestCheckSecretsRejectsLiterals(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantErr string
	}{
		{
			name: "literal postgres password",
			cfg: &Config{Primary: SiteConfig{Postgres: PostgresConfig{
				Password: "hunter2", ReplicationPassword: "${PG_REPL}"}}},
			wantErr: "primary.postgres.password",
		},
		{
			name:    "literal webhook token",
			cfg:     &Config{Webhook: &WebhookConfig{SecretToken: "WEBHOOK_SECRET_PLACEHOLDER"}},
			wantErr: "webhook.secret_token",
		},
		{
			name: "literal s3 secret in a secondary",
			cfg: &Config{Secondaries: []SiteConfig{{ObjectStore: ObjectStoreConfig{
				S3: &S3Config{AccessKey: "${S3_AK}", SecretKey: "raw-secret"}}}}},
			wantErr: "secondaries[0].object_storage.s3.secret_key",
		},
		{
			name: "literal api token",
			cfg: &Config{APIValidator: &APIValidatorConfig{
				PrimaryToken: "${P}", SecondaryToken: "glpat-literal"}},
			wantErr: "api_validator.secondary_token",
		},
		{
			name:    "partial reference is still a literal",
			cfg:     &Config{Webhook: &WebhookConfig{SecretToken: "prefix-${TOKEN}"}},
			wantErr: "webhook.secret_token",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSecretsAreEnvRefs(tc.cfg)
			if err == nil {
				t.Fatal("expected a literal-secret error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q should name %q", err, tc.wantErr)
			}
		})
	}
}

func TestCheckSecretsAcceptsEnvRefsAndEmpty(t *testing.T) {
	cfg := &Config{
		Primary: SiteConfig{Postgres: PostgresConfig{
			Password: "${PG_CTRL}", ReplicationPassword: "${PG_REPL}"}},
		Secondaries: []SiteConfig{{ObjectStore: ObjectStoreConfig{
			S3: &S3Config{AccessKey: "${S3_AK}", SecretKey: "${S3_SK}"}}}},
		Webhook: &WebhookConfig{SecretToken: "${WEBHOOK_SECRET_TOKEN}"},
	}
	if err := checkSecretsAreEnvRefs(cfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckSecretsReportsEveryViolation(t *testing.T) {
	cfg := &Config{Primary: SiteConfig{Postgres: PostgresConfig{
		Password: "a", ReplicationPassword: "b"}}}
	err := checkSecretsAreEnvRefs(cfg)
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{"primary.postgres.password", "primary.postgres.replication_password"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %q, got: %v", want, err)
		}
	}
}

func TestLoadRejectsLiteralSecret(t *testing.T) {
	yaml := `
primary:
  name: p
  external_url: https://p.example.com
  postgres:
    host: 10.0.0.10
    port: 5432
    password: plaintext-password
    replication_user: repl
    replication_password: ${PG_REPL_PASSWORD}
  git:
    mode: rsync
    repos_path: /repos
secondaries:
  - name: s
    postgres:
      host: 10.1.0.10
      replication_password: ${SEC_REPL_PASSWORD}
`
	t.Setenv("PG_REPL_PASSWORD", "x")
	t.Setenv("SEC_REPL_PASSWORD", "y")
	_, err := Load(writeTempConfig(t, yaml))
	if err == nil {
		t.Fatal("expected Load to reject a literal secret")
	}
	if !strings.Contains(err.Error(), "primary.postgres.password") {
		t.Errorf("error should name the offending field, got: %v", err)
	}
}
func TestValidatePaths(t *testing.T) {
	tests := []struct {
		name    string
		site    SiteConfig
		wantErr string
	}{
		{name: "absolute paths", site: SiteConfig{
			Git:         GitStorage{ReposPath: "/var/opt/gitlab/git-data/repositories"},
			ObjectStore: ObjectStoreConfig{FSPaths: []string{"/var/opt/gitlab/uploads"}},
		}},
		{name: "empty is fine", site: SiteConfig{}},
		{
			name:    "relative repos_path",
			site:    SiteConfig{Git: GitStorage{ReposPath: "repositories"}},
			wantErr: "primary.git.repos_path",
		},
		{
			name:    "relative fs_path",
			site:    SiteConfig{ObjectStore: ObjectStoreConfig{FSPaths: []string{"/ok", "uploads"}}},
			wantErr: "primary.object_storage.fs_paths[1]",
		},
		{
			name:    "newline in path",
			site:    SiteConfig{Git: GitStorage{ReposPath: "/repos\nrm -rf /"}},
			wantErr: "NUL or newline",
		},
		{
			name:    "relative registry fs_path",
			site:    SiteConfig{Registry: &RegistryConfig{FSPath: "registry"}},
			wantErr: "primary.registry.fs_path",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{Primary: tc.site}
			var errs []error
			c.validatePaths(&errs)
			joined := errors.Join(errs...)
			if tc.wantErr == "" {
				if joined != nil {
					t.Fatalf("unexpected errors: %v", joined)
				}
				return
			}
			if joined == nil {
				t.Fatal("expected a path validation error")
			}
			if !strings.Contains(joined.Error(), tc.wantErr) {
				t.Errorf("error %q should mention %q", joined, tc.wantErr)
			}
		})
	}
}
func TestValidateSSHPolicy(t *testing.T) {
	tests := []struct {
		name    string
		ssh     SSHConfig
		wantErr bool
	}{
		{name: "unset", ssh: SSHConfig{}},
		{name: "yes", ssh: SSHConfig{StrictHostKeyChecking: "yes"}},
		{name: "no", ssh: SSHConfig{StrictHostKeyChecking: "no"}},
		{name: "accept-new", ssh: SSHConfig{StrictHostKeyChecking: "accept-new"}},
		{name: "known_hosts implies yes", ssh: SSHConfig{KnownHostsFile: "/etc/known_hosts"}},
		{name: "typo", ssh: SSHConfig{StrictHostKeyChecking: "ture"}, wantErr: true},
		{name: "shell-ish", ssh: SSHConfig{StrictHostKeyChecking: "yes -o ProxyCommand=x"}, wantErr: true},
		{name: "wrong case", ssh: SSHConfig{StrictHostKeyChecking: "Yes"}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{SSH: tc.ssh}
			var errs []error
			c.validateSSHPolicy(&errs)
			if tc.wantErr && len(errs) == 0 {
				t.Error("expected a validation error")
			}
			if !tc.wantErr && len(errs) != 0 {
				t.Errorf("unexpected errors: %v", errs)
			}
		})
	}
}
func TestValidateExternalURLs(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr string
	}{
		{name: "https", url: "https://gitlab.example.com"},
		{name: "https with port", url: "https://gitlab.example.com:8443"},
		{name: "http warns but loads", url: "http://gitlab.example.com"},
		{name: "empty is handled elsewhere", url: ""},
		{name: "ftp scheme", url: "ftp://gitlab.example.com", wantErr: "must use http or https"},
		{name: "no scheme", url: "gitlab.example.com", wantErr: "must use http or https"},
		{name: "no host", url: "https://", wantErr: "has no host"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{Primary: SiteConfig{ExternalURL: tc.url}}
			var errs []error
			c.validateExternalURLs(&errs)
			joined := errors.Join(errs...)
			if tc.wantErr == "" {
				if joined != nil {
					t.Fatalf("unexpected errors: %v", joined)
				}
				return
			}
			if joined == nil {
				t.Fatal("expected a validation error")
			}
			if !strings.Contains(joined.Error(), tc.wantErr) {
				t.Errorf("error %q should mention %q", joined, tc.wantErr)
			}
		})
	}
}

func TestValidateWebhookTLS(t *testing.T) {
	tests := []struct {
		name    string
		hook    *WebhookConfig
		wantErr bool
	}{
		{name: "no webhook block", hook: nil},
		{name: "plaintext warns only", hook: &WebhookConfig{Addr: ":9102"}},
		{name: "both set", hook: &WebhookConfig{TLSCert: "/c.pem", TLSKey: "/k.pem"}},
		{name: "cert without key", hook: &WebhookConfig{TLSCert: "/c.pem"}, wantErr: true},
		{name: "key without cert", hook: &WebhookConfig{TLSKey: "/k.pem"}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &Config{Webhook: tc.hook}
			var errs []error
			c.validateWebhookTLS(&errs)
			if tc.wantErr && len(errs) == 0 {
				t.Error("expected a validation error")
			}
			if !tc.wantErr && len(errs) != 0 {
				t.Errorf("unexpected errors: %v", errs)
			}
		})
	}
}

func TestMetricsAddrDefaultsToLoopback(t *testing.T) {
	c := &Config{}
	_ = c.validate()
	if c.Metrics.Addr != "127.0.0.1:9101" {
		t.Errorf("metrics.addr = %q, want 127.0.0.1:9101 — /metrics has no auth", c.Metrics.Addr)
	}
}
