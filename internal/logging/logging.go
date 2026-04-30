// Package logging constructs the application's *slog.Logger from environment variables.
package logging

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
)

const (
	envLevel   = "LOG_LEVEL"
	envFormat  = "LOG_FORMAT"
	formatJSON = "json"
	formatText = "text"
)

// New constructs a *slog.Logger configured from LOG_LEVEL and LOG_FORMAT
// environment variables. Output goes to stderr.
//
// Valid LOG_LEVEL values (case-insensitive): debug, info, warn, error. Default: info.
// Valid LOG_FORMAT values (case-insensitive): text, json. Default: text.
//
// On invalid values it returns a non-nil logger using defaults along with a
// descriptive error so the caller can warn and proceed.
func New() (*slog.Logger, error) {
	var errs []string

	level, lvlErr := parseLevel(os.Getenv(envLevel))
	if lvlErr != nil {
		errs = append(errs, lvlErr.Error())
	}

	format, fmtErr := parseFormat(os.Getenv(envFormat))
	if fmtErr != nil {
		errs = append(errs, fmtErr.Error())
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	switch format {
	case formatJSON:
		handler = slog.NewJSONHandler(os.Stderr, opts)
	default:
		handler = slog.NewTextHandler(os.Stderr, opts)
	}

	logger := slog.New(handler)
	if len(errs) > 0 {
		return logger, fmt.Errorf("logging configuration: %s", strings.Join(errs, "; "))
	}
	return logger, nil
}

func parseLevel(s string) (slog.Level, error) {
	if s == "" {
		return slog.LevelInfo, nil
	}
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("invalid LOG_LEVEL %q (using info)", s)
	}
}

func parseFormat(s string) (string, error) {
	if s == "" {
		return formatText, nil
	}
	switch strings.ToLower(s) {
	case formatText:
		return formatText, nil
	case formatJSON:
		return formatJSON, nil
	default:
		return formatText, fmt.Errorf("invalid LOG_FORMAT %q (using text)", s)
	}
}
