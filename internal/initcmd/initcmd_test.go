package initcmd

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestGenerateYAMLS3(t *testing.T) {
	a := &Answers{
		PrimaryName:          "primary",
		PrimaryURL:           "https://gitlab.primary.example.com",
		PrimarySSHHost:       "primary.example.com:22",
		PrimaryPGHost:        "10.0.0.10",
		PrimaryPGPort:        "5432",
		PrimaryPGDB:          "gitlabhq_production",
		PrimaryPGUser:        "gitlab",
		PrimaryReplUser:      "gitlab_repl",
		PrimarySlot:          "geo_slot",
		PrimaryGitMode:       "rsync",
		PrimaryReposPath:     "/var/opt/gitlab/git-data/repositories",
		SecondaryName:        "secondary",
		SecondaryURL:         "https://gitlab.secondary.example.com",
		SecondarySSHHost:     "secondary.example.com:22",
		SecondaryPGHost:      "10.1.0.10",
		SecondaryPGPort:      "5432",
		SecondaryReposPath:   "/var/opt/gitlab/git-data/repositories",
		ObjectStorageBackend: "s3",
		S3Region:             "eu-west-1",
		S3PrimaryBucket:      "gitlab-primary",
		S3ReplicaBucket:      "gitlab-replica",
		S3Endpoint:           "http://minio:9000",
		FailoverEnabled:      true,
		ReadOnlySecondary:    true,
	}
	var buf bytes.Buffer
	if err := GenerateYAML(a, &buf); err != nil {
		t.Fatalf("GenerateYAML: %v", err)
	}
	out := buf.String()
	required := []string{
		"name: primary",
		"external_url: https://gitlab.primary.example.com",
		"password: ${PG_CTRL_PASSWORD}",
		"replication_password: ${PG_REPL_PASSWORD}",
		"slot_name: geo_slot",
		"mode: rsync",
		"backend: s3",
		"region: eu-west-1",
		"primary_bucket: gitlab-primary",
		"endpoint: http://minio:9000",
		"name: secondary",
		"failover_enabled: true",
		"read_only_secondary: true",
		"sweep_interval: 5m",
		"control_db: sqlite://data/syncctl.db",
	}
	for _, s := range required {
		if !strings.Contains(out, s) {
			t.Errorf("missing in output: %q", s)
		}
	}
}

func TestGenerateYAMLFS(t *testing.T) {
	a := &Answers{
		PrimaryName:          "primary",
		PrimaryURL:           "https://p.example.com",
		PrimarySSHHost:       "p:22",
		PrimaryPGHost:        "h",
		PrimaryPGPort:        "5432",
		PrimaryPGDB:          "gitlabhq_production",
		PrimaryPGUser:        "gitlab",
		PrimaryReplUser:      "gitlab_repl",
		PrimaryGitMode:       "rsync",
		PrimaryReposPath:     "/var/opt/gitlab/git-data/repositories",
		SecondaryName:        "secondary",
		SecondaryURL:         "https://s.example.com",
		SecondarySSHHost:     "s:22",
		SecondaryPGHost:      "h",
		SecondaryPGPort:      "5432",
		SecondaryReposPath:   "/var/opt/gitlab/git-data/repositories",
		ObjectStorageBackend: "fs",
	}
	var buf bytes.Buffer
	if err := GenerateYAML(a, &buf); err != nil {
		t.Fatalf("GenerateYAML: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "fs_paths:") {
		t.Error("fs_paths should be present for fs backend")
	}
	if !strings.Contains(out, "/var/opt/gitlab/uploads") {
		t.Error("default fs paths should include uploads")
	}
	if strings.Contains(out, "endpoint:") {
		t.Error("endpoint should not be present for fs backend")
	}
}

func TestGenerateYAMLSkipsSlotWhenEmpty(t *testing.T) {
	a := &Answers{
		PrimaryName:          "primary",
		PrimaryURL:           "https://p.example.com",
		PrimarySSHHost:       "p:22",
		PrimaryPGHost:        "h",
		PrimaryPGPort:        "5432",
		PrimaryPGDB:          "gitlabhq_production",
		PrimaryPGUser:        "gitlab",
		PrimaryReplUser:      "gitlab_repl",
		PrimaryGitMode:       "rsync",
		PrimaryReposPath:     "/r",
		SecondaryName:        "secondary",
		SecondaryURL:         "https://s.example.com",
		SecondarySSHHost:     "s:22",
		SecondaryPGHost:      "h",
		SecondaryPGPort:      "5432",
		SecondaryReposPath:   "/r",
		ObjectStorageBackend: "s3",
		S3Region:             "r",
		S3PrimaryBucket:      "p",
		S3ReplicaBucket:      "s",
	}
	var buf bytes.Buffer
	if err := GenerateYAML(a, &buf); err != nil {
		t.Fatalf("GenerateYAML: %v", err)
	}
	if strings.Contains(buf.String(), "slot_name:") {
		t.Error("slot_name should be omitted when PrimarySlot is empty")
	}
}

func TestPromptUsesDefault(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("\n"))
	var w bytes.Buffer
	got := prompt(r, &w, "label", "default")
	if got != "default" {
		t.Errorf("got %q, want default", got)
	}
}

func TestPromptUsesInput(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("custom\n"))
	var w bytes.Buffer
	got := prompt(r, &w, "label", "default")
	if got != "custom" {
		t.Errorf("got %q, want custom", got)
	}
}

func TestPromptTrimsWhitespace(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("  spaces  \n"))
	var w bytes.Buffer
	got := prompt(r, &w, "label", "default")
	if got != "spaces" {
		t.Errorf("got %q, want 'spaces'", got)
	}
}

func TestConfirmDefaultTrue(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("\n"))
	var w bytes.Buffer
	got := confirm(r, &w, "ok?", true)
	if !got {
		t.Error("empty input should return default (true)")
	}
}

func TestConfirmDefaultFalse(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("\n"))
	var w bytes.Buffer
	got := confirm(r, &w, "ok?", false)
	if got {
		t.Error("empty input should return default (false)")
	}
}

func TestConfirmYes(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("y\n"))
	var w bytes.Buffer
	got := confirm(r, &w, "ok?", false)
	if !got {
		t.Error("'y' should return true")
	}
}

func TestConfirmYesFull(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("yes\n"))
	var w bytes.Buffer
	got := confirm(r, &w, "ok?", false)
	if !got {
		t.Error("'yes' should return true")
	}
}

func TestConfirmNo(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("n\n"))
	var w bytes.Buffer
	got := confirm(r, &w, "ok?", true)
	if got {
		t.Error("'n' should return false")
	}
}

func TestConfirmCaseInsensitive(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("YES\n"))
	var w bytes.Buffer
	got := confirm(r, &w, "ok?", false)
	if !got {
		t.Error("'YES' should return true (case-insensitive)")
	}
}

func TestRunInteractive(t *testing.T) {
	// Simulate user pressing enter for every prompt (accepting defaults).
	input := strings.Repeat("\n", 20)
	_, err := RunWithInput(strings.NewReader(input), &bytes.Buffer{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRunCustomInput(t *testing.T) {
	// Provide custom values for each prompt.
	input := strings.NewReader("myprimary\nhttps://my.gitlab.com\nmyhost:22\n10.0.0.1\n5432\nmydb\nmyuser\nmyrepl\nmyslot\nfetch\n/my/repos\nmysecondary\nhttps://my.secondary.com\nmysec:22\n10.1.0.1\n5432\n/my/repos\ns3\nus-east-1\nmybucket\nmyreplica\nhttp://minio:9000\nn\ny\n")
	var w bytes.Buffer
	a, err := RunWithInput(input, &w)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if a.PrimaryName != "myprimary" {
		t.Errorf("PrimaryName = %q", a.PrimaryName)
	}
	if a.PrimaryURL != "https://my.gitlab.com" {
		t.Errorf("PrimaryURL = %q", a.PrimaryURL)
	}
	if a.PrimaryGitMode != "fetch" {
		t.Errorf("PrimaryGitMode = %q", a.PrimaryGitMode)
	}
	if a.ObjectStorageBackend != "s3" {
		t.Errorf("ObjectStorageBackend = %q", a.ObjectStorageBackend)
	}
	if !a.FailoverEnabled {
		t.Error("FailoverEnabled should be true (answered y)")
	}
	if a.ReadOnlySecondary {
		t.Error("ReadOnlySecondary should be false (answered n)")
	}
}

func TestRunFSBackendSkipsS3Prompts(t *testing.T) {
	input := strings.NewReader("p\nhttps://p.com\np:22\n10.0.0.1\n5432\ndb\nuser\nrepl\n\nrsync\n/repos\ns\nhttps://s.com\ns:22\n10.1.0.1\n5432\n/repos\nfs\nn\nn\n")
	var w bytes.Buffer
	a, err := RunWithInput(input, &w)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if a.ObjectStorageBackend != "fs" {
		t.Errorf("ObjectStorageBackend = %q, want fs", a.ObjectStorageBackend)
	}
	if a.S3Region != "" {
		t.Errorf("S3Region should be empty for fs backend: %q", a.S3Region)
	}
}
