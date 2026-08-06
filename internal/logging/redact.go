package logging

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync/atomic"
)

// redactPaths controls whether project and repository paths are hashed
// before they reach a log line. It is process-global because logging is,
// and it is read on every log call rather than plumbed through every
// reconciler.
var redactPaths atomic.Bool

// SetRedactProjectPaths turns project-path redaction on or off. Set from
// config at startup.
func SetRedactProjectPaths(on bool) { redactPaths.Store(on) }

// RedactProjectPathsEnabled reports the current setting.
func RedactProjectPathsEnabled() bool { return redactPaths.Load() }

// ProjectPath renders a project, group or repository path for logging.
//
// Group and project names are frequently confidential in themselves —
// customer names, unannounced products, internal codenames — and logs
// are routinely shipped to third-party aggregators. When redaction is
// on, the path becomes a stable short digest so failures can still be
// correlated across log lines without the name leaving the operator's
// infrastructure.
func ProjectPath(path string) string {
	if path == "" || !redactPaths.Load() {
		return path
	}
	sum := sha256.Sum256([]byte(path))
	return "redacted:" + hex.EncodeToString(sum[:])[:12]
}

// maxOutputLog bounds a captured command transcript in a log line.
// git and rsync failure output is unbounded and can carry branch names,
// object IDs and filenames from inside a repository; a warning needs to
// say that something failed and where, not reproduce the transcript.
const maxOutputLog = 200

// CommandOutput trims a command transcript to a length suitable for a
// warning. The untruncated text belongs at debug level.
func CommandOutput(out string) string {
	out = strings.TrimSpace(out)
	if len(out) <= maxOutputLog {
		return out
	}
	return out[:maxOutputLog] + "… (truncated; run with --log-level debug for the full output)"
}
