package logging

import (
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestNew_Defaults(t *testing.T) {
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("LOG_FORMAT", "")

	logger, err := New()
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	if logger == nil {
		t.Fatal("New() returned nil logger")
	}
	ctx := context.Background()
	if !logger.Handler().Enabled(ctx, slog.LevelInfo) {
		t.Error("info level should be enabled by default")
	}
	if logger.Handler().Enabled(ctx, slog.LevelDebug) {
		t.Error("debug level should not be enabled by default")
	}
}

func TestNew_ValidValues(t *testing.T) {
	cases := []struct {
		name    string
		level   string
		format  string
		debugOn bool
		warnOn  bool
		errorOn bool
	}{
		{name: "debug_text", level: "debug", format: "text", debugOn: true, warnOn: true, errorOn: true},
		{name: "info_json", level: "info", format: "json", debugOn: false, warnOn: true, errorOn: true},
		{name: "warn_uppercase", level: "WARN", format: "JSON", debugOn: false, warnOn: true, errorOn: true},
		{name: "error_mixed", level: "Error", format: "Text", debugOn: false, warnOn: false, errorOn: true},
		{name: "warning_alias", level: "warning", format: "text", debugOn: false, warnOn: true, errorOn: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LOG_LEVEL", tc.level)
			t.Setenv("LOG_FORMAT", tc.format)

			logger, err := New()
			if err != nil {
				t.Fatalf("New() error = %v, want nil", err)
			}
			if logger == nil {
				t.Fatal("logger is nil")
			}
			ctx := context.Background()
			if got := logger.Handler().Enabled(ctx, slog.LevelDebug); got != tc.debugOn {
				t.Errorf("debug enabled = %v, want %v", got, tc.debugOn)
			}
			if got := logger.Handler().Enabled(ctx, slog.LevelWarn); got != tc.warnOn {
				t.Errorf("warn enabled = %v, want %v", got, tc.warnOn)
			}
			if got := logger.Handler().Enabled(ctx, slog.LevelError); got != tc.errorOn {
				t.Errorf("error enabled = %v, want %v", got, tc.errorOn)
			}
		})
	}
}

func TestNew_InvalidValues(t *testing.T) {
	t.Setenv("LOG_LEVEL", "notalevel")
	t.Setenv("LOG_FORMAT", "notaformat")

	logger, err := New()
	if err == nil {
		t.Fatal("expected error for invalid values, got nil")
	}
	if logger == nil {
		t.Fatal("expected non-nil logger fallback, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"LOG_LEVEL", "LOG_FORMAT"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
	// Logger should still work at default info level.
	ctx := context.Background()
	if !logger.Handler().Enabled(ctx, slog.LevelInfo) {
		t.Error("fallback logger should have info enabled")
	}
}
