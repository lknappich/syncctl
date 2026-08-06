// Package initcmd implements `syncctl init` — an interactive wizard that
// generates a config.yaml by asking questions about the primary and
// secondary GitLab instances.
package initcmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// Answers holds the user's responses.
type Answers struct {
	PrimaryName      string
	PrimaryURL       string
	PrimarySSHHost   string
	PrimaryPGHost    string
	PrimaryPGPort    string
	PrimaryPGDB      string
	PrimaryPGUser    string
	PrimaryReplUser  string
	PrimarySlot      string
	PrimaryGitMode   string
	PrimaryReposPath string

	SecondaryName      string
	SecondaryURL       string
	SecondarySSHHost   string
	SecondaryPGHost    string
	SecondaryPGPort    string
	SecondaryReposPath string

	ObjectStorageBackend string
	S3Region             string
	S3PrimaryBucket      string
	S3ReplicaBucket      string
	S3Endpoint           string

	FailoverEnabled   bool
	ReadOnlySecondary bool
}

// Run prompts the user for each field and writes a config.yaml.
func Run(w io.Writer) (*Answers, error) {
	return RunWithInput(os.Stdin, w)
}

// RunWithInput is like Run but reads from the provided reader instead
// of os.Stdin. Used by tests to inject canned input.
func RunWithInput(r io.Reader, w io.Writer) (*Answers, error) {
	reader := bufio.NewReader(r)
	a := &Answers{}

	out := func(format string, args ...any) {
		_, _ = fmt.Fprintf(w, format, args...)
	}
	out("\n=== syncctl configuration wizard ===\n\n")
	out("This will generate a config.yaml. All secrets will be\n")
	out("referenced as ${ENV_VAR} placeholders — export them before running syncctl.\n")

	// --- Primary ---
	out("\n--- PRIMARY ---\n")
	a.PrimaryName = prompt(reader, w, "Primary site name", "primary")
	a.PrimaryURL = prompt(reader, w, "Primary external URL", "https://gitlab.primary.example.com")
	a.PrimarySSHHost = prompt(reader, w, "Primary SSH host:port", "primary.example.com:22")
	a.PrimaryPGHost = prompt(reader, w, "Primary PostgreSQL host", "10.0.0.10")
	a.PrimaryPGPort = prompt(reader, w, "Primary PostgreSQL port", "5432")
	a.PrimaryPGDB = prompt(reader, w, "Primary PostgreSQL database", "gitlabhq_production")
	a.PrimaryPGUser = prompt(reader, w, "Primary PostgreSQL control user", "gitlab")
	a.PrimaryReplUser = prompt(reader, w, "Primary PostgreSQL replication user", "gitlab_repl")
	a.PrimarySlot = prompt(reader, w, "Replication slot name (blank for none)", "")
	a.PrimaryGitMode = prompt(reader, w, "Git data sync mode (rsync|fetch)", "rsync")
	a.PrimaryReposPath = prompt(reader, w, "Primary repos path", "/var/opt/gitlab/git-data/repositories")

	// --- Secondary ---
	out("\n--- SECONDARY ---\n")
	a.SecondaryName = prompt(reader, w, "Secondary site name", "secondary")
	a.SecondaryURL = prompt(reader, w, "Secondary external URL", "https://gitlab.secondary.example.com")
	a.SecondarySSHHost = prompt(reader, w, "Secondary SSH host:port", "secondary.example.com:22")
	a.SecondaryPGHost = prompt(reader, w, "Secondary PostgreSQL host", "10.1.0.10")
	a.SecondaryPGPort = prompt(reader, w, "Secondary PostgreSQL port", "5432")
	a.SecondaryReposPath = prompt(reader, w, "Secondary repos path", "/var/opt/gitlab/git-data/repositories")

	// --- Object storage ---
	out("\n--- OBJECT STORAGE ---\n")
	a.ObjectStorageBackend = prompt(reader, w, "Object storage backend (s3|fs)", "s3")
	if a.ObjectStorageBackend == "s3" {
		a.S3Region = prompt(reader, w, "S3 region", "eu-west-1")
		a.S3PrimaryBucket = prompt(reader, w, "S3 primary bucket", "gitlab-primary")
		a.S3ReplicaBucket = prompt(reader, w, "S3 replica bucket", "gitlab-replica")
		a.S3Endpoint = prompt(reader, w, "S3 endpoint (blank for AWS, http://minio:9000 for MinIO)", "")
	}

	// --- Options ---
	out("\n--- OPTIONS ---\n")
	a.ReadOnlySecondary = confirm(reader, w, "Enforce read-only mode on secondary?", true)
	a.FailoverEnabled = confirm(reader, w, "Enable failover controller?", false)

	return a, nil
}

// GenerateYAML writes the config.yaml from answers.
func GenerateYAML(a *Answers, w io.Writer) error {
	var b strings.Builder

	// --- Primary ---
	b.WriteString("primary:\n")
	b.WriteString(fmt.Sprintf("  name: %s\n", a.PrimaryName))
	b.WriteString(fmt.Sprintf("  external_url: %s\n", a.PrimaryURL))
	b.WriteString(fmt.Sprintf("  ssh_host: %s\n", a.PrimarySSHHost))
	b.WriteString("  postgres:\n")
	b.WriteString(fmt.Sprintf("    host: %s\n", a.PrimaryPGHost))
	b.WriteString(fmt.Sprintf("    port: %s\n", a.PrimaryPGPort))
	b.WriteString(fmt.Sprintf("    db: %s\n", a.PrimaryPGDB))
	b.WriteString(fmt.Sprintf("    user: %s\n", a.PrimaryPGUser))
	b.WriteString("    password: ${PG_CTRL_PASSWORD}\n")
	b.WriteString(fmt.Sprintf("    replication_user: %s\n", a.PrimaryReplUser))
	b.WriteString("    replication_password: ${PG_REPL_PASSWORD}\n")
	b.WriteString("    sslmode: require\n")
	if a.PrimarySlot != "" {
		b.WriteString(fmt.Sprintf("    slot_name: %s\n", a.PrimarySlot))
	}
	b.WriteString("  git:\n")
	b.WriteString(fmt.Sprintf("    mode: %s\n", a.PrimaryGitMode))
	b.WriteString(fmt.Sprintf("    repos_path: %s\n", a.PrimaryReposPath))
	b.WriteString("    hashed_storage: true\n")
	b.WriteString("  object_storage:\n")
	b.WriteString(fmt.Sprintf("    backend: %s\n", a.ObjectStorageBackend))
	if a.ObjectStorageBackend == "s3" {
		b.WriteString("    s3:\n")
		b.WriteString(fmt.Sprintf("      region: %s\n", a.S3Region))
		b.WriteString(fmt.Sprintf("      primary_bucket: %s\n", a.S3PrimaryBucket))
		b.WriteString(fmt.Sprintf("      replica_bucket: %s\n", a.S3ReplicaBucket))
		b.WriteString("      access_key: ${S3_AK}\n")
		b.WriteString("      secret_key: ${S3_SK}\n")
		if a.S3Endpoint != "" {
			b.WriteString(fmt.Sprintf("      endpoint: %s\n", a.S3Endpoint))
		}
	} else {
		b.WriteString("    fs_paths:\n")
		b.WriteString("      - /var/opt/gitlab/uploads\n")
		b.WriteString("      - /var/opt/gitlab/artifacts\n")
		b.WriteString("      - /var/opt/gitlab/packages\n")
		b.WriteString("      - /var/opt/gitlab/lfs-objects\n")
	}

	// --- Secondary ---
	b.WriteString("\nsecondaries:\n")
	b.WriteString(fmt.Sprintf("  - name: %s\n", a.SecondaryName))
	b.WriteString(fmt.Sprintf("    external_url: %s\n", a.SecondaryURL))
	b.WriteString(fmt.Sprintf("    ssh_host: %s\n", a.SecondarySSHHost))
	b.WriteString("    postgres:\n")
	b.WriteString(fmt.Sprintf("      host: %s\n", a.SecondaryPGHost))
	b.WriteString(fmt.Sprintf("      port: %s\n", a.SecondaryPGPort))
	b.WriteString(fmt.Sprintf("      db: %s\n", a.PrimaryPGDB))
	b.WriteString(fmt.Sprintf("      user: %s\n", a.PrimaryPGUser))
	b.WriteString("      password: ${PG_CTRL_PASSWORD}\n")
	b.WriteString(fmt.Sprintf("      replication_user: %s\n", a.PrimaryReplUser))
	b.WriteString("      replication_password: ${SEC_REPL_PASSWORD}\n")
	b.WriteString("    git:\n")
	b.WriteString(fmt.Sprintf("      mode: %s\n", a.PrimaryGitMode))
	b.WriteString(fmt.Sprintf("      repos_path: %s\n", a.SecondaryReposPath))
	b.WriteString("      hashed_storage: true\n")
	b.WriteString("    object_storage:\n")
	b.WriteString(fmt.Sprintf("      backend: %s\n", a.ObjectStorageBackend))
	if a.ObjectStorageBackend == "s3" {
		b.WriteString("      s3:\n")
		b.WriteString(fmt.Sprintf("        region: %s\n", a.S3Region))
		b.WriteString(fmt.Sprintf("        primary_bucket: %s\n", a.S3PrimaryBucket))
		b.WriteString(fmt.Sprintf("        replica_bucket: %s\n", a.S3ReplicaBucket))
		b.WriteString("        access_key: ${S3_AK}\n")
		b.WriteString("        secret_key: ${S3_SK}\n")
	}

	// --- Sync ---
	b.WriteString("\nsync:\n")
	b.WriteString("  sweep_interval: 5m\n")
	b.WriteString("  lag_warning_threshold: 30s\n")
	b.WriteString("  lag_critical_threshold: 5m\n")
	b.WriteString(fmt.Sprintf("  failover_enabled: %t\n", a.FailoverEnabled))
	b.WriteString(fmt.Sprintf("  read_only_secondary: %t\n", a.ReadOnlySecondary))
	b.WriteString("  consistency_sample_pct: 0.01\n")

	// --- Metrics/Log ---
	b.WriteString("\nmetrics:\n")
	b.WriteString("  addr: \":9101\"\n")
	b.WriteString("\nlog:\n")
	b.WriteString("  level: info\n")
	b.WriteString("  format: json\n")

	_, err := io.WriteString(w, b.String())
	return err
}

func prompt(r *bufio.Reader, w io.Writer, label, def string) string {
	if def != "" {
		_, _ = fmt.Fprintf(w, "%s [%s]: ", label, def)
	} else {
		_, _ = fmt.Fprintf(w, "%s: ", label)
	}
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func confirm(r *bufio.Reader, w io.Writer, label string, def bool) bool {
	defStr := "y"
	if !def {
		defStr = "n"
	}
	_, _ = fmt.Fprintf(w, "%s [y/n, default=%s]: ", label, defStr)
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" {
		return def
	}
	return line == "y" || line == "yes"
}
