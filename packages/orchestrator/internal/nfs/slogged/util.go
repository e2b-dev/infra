package slogged

import (
	"fmt"
	"log/slog"
)

func slogStart(s string, args ...any) {
	slog.Debug("Starting "+s, "args", args)
}

func slogEnd(s string, args ...any) {
	slogEndWithError(s, nil, args...)
}

func slogEndWithError(s string, err error, args ...any) {
	if err == nil {
		slog.Debug("Finishing "+s, "return", args)
		return
	}

	slog.Error(fmt.Sprintf("Error in %s", s), "error", err)
}
