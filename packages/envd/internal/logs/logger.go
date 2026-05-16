package logs

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"

	"github.com/e2b-dev/infra/packages/envd/internal/host"
	"github.com/e2b-dev/infra/packages/envd/internal/logs/exporter"
)

func NewLogger(ctx context.Context, isNotFC bool, verbose bool, mmdsChan <-chan *host.MMDSOpts) *zerolog.Logger {
	zerolog.TimestampFieldName = "timestamp"
	zerolog.TimeFieldFormat = time.RFC3339Nano

	exporters := []io.Writer{}

	if !isNotFC {
		exporters = append(exporters, exporter.NewHTTPLogsExporter(ctx, isNotFC, mmdsChan))
	}
	// Stdout is opt-in via -verbose. Inside FC stdout flows into journald and
	// dirties guest pages on every snapshot, so we keep it off by default and
	// rely on the HTTP exporter to ship debug logs to the orchestrator.
	if verbose {
		exporters = append(exporters, os.Stdout)
	}

	l := zerolog.
		New(io.MultiWriter(exporters...)).
		With().
		Timestamp().
		Logger().
		Level(zerolog.DebugLevel)

	return &l
}
