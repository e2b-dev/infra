package logger

import (
	"path/filepath"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestTracedLoggerCallerSkipsWrapper(t *testing.T) {
	t.Parallel()

	core, logs := observer.New(zap.InfoLevel)
	logger := NewTracedLogger(zap.New(core)) //nolint:forbidigo // test needs a raw zap.Logger with observable core

	logger.Warn(t.Context(), "wrapper skip test")

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(entries))
	}

	entry := entries[0]
	if got := filepath.Base(entry.Caller.File); got != "logger_test.go" {
		t.Fatalf("expected caller file logger_test.go, got %q", entry.Caller.File)
	}
	if entry.Caller.Line == 0 {
		t.Fatal("expected caller line to be set")
	}
}

func TestLAfterReplaceGlobalsCallerIsCorrect(t *testing.T) {
	t.Parallel()

	core, logs := observer.New(zap.InfoLevel)
	logger := NewTracedLogger(zap.New(core)) //nolint:forbidigo // test needs a raw zap.Logger with observable core

	undo := ReplaceGlobals(t.Context(), logger)
	defer undo()

	// L() wraps zap.L() which already has AddCallerSkip(1) from ReplaceGlobals.
	// This test verifies L() does not double-apply the skip.
	L().Info(t.Context(), "global logger test")

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("expected 1 log entry, got %d", len(entries))
	}

	entry := entries[0]
	if got := filepath.Base(entry.Caller.File); got != "logger_test.go" {
		t.Fatalf("expected caller file logger_test.go, got %q (double AddCallerSkip?)", entry.Caller.File)
	}
	if entry.Caller.Line == 0 {
		t.Fatal("expected caller line to be set")
	}
}
