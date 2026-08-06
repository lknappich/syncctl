// Package logging provides a shared structured logger backed by zerolog.
package logging

import (
	"io"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Format selects log output encoding.
type Format string

const (
	FormatJSON Format = "json"
	FormatText Format = "text" // human-readable, colorized when TTY
)

// Configure initializes the global zerolog logger.
// level: "trace"|"debug"|"info"|"warn"|"error" (case-insensitive).
// format: "json"|"text".
func Configure(level, format string) zerolog.Logger {
	return ConfigureWith(level, format, RedactProjectPathsEnabled())
}

// ConfigureWith is Configure plus the project-path redaction setting.
func ConfigureWith(level, format string, redactProjectPaths bool) zerolog.Logger {
	SetRedactProjectPaths(redactProjectPaths)
	// Defaults: JSON to stderr, info level.
	zerolog.TimeFieldFormat = time.RFC3339Nano
	zerolog.SetGlobalLevel(parseLevel(level))

	var w io.Writer = os.Stderr
	if f := parseFormat(format); f == FormatText {
		w = zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}
	}

	logger := zerolog.New(w).With().Timestamp().Caller().Logger()
	log.Logger = logger
	return logger
}

func parseLevel(s string) zerolog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "trace":
		return zerolog.TraceLevel
	case "debug":
		return zerolog.DebugLevel
	case "warn", "warning":
		return zerolog.WarnLevel
	case "error":
		return zerolog.ErrorLevel
	case "fatal":
		return zerolog.FatalLevel
	case "panic":
		return zerolog.PanicLevel
	case "", "info":
		return zerolog.InfoLevel
	default:
		return zerolog.InfoLevel
	}
}

func parseFormat(s string) Format {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "text", "console", "pretty":
		return FormatText
	default:
		return FormatJSON
	}
}
