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

func NewLogger(ctx context.Context, isFC bool, verbose bool, mmdsChan <-chan *host.MMDSOpts) *zerolog.Logger {
	zerolog.TimestampFieldName = "timestamp"
	zerolog.TimeFieldFormat = time.RFC3339Nano

	exporters := []io.Writer{}

	if isFC {
		exporters = append(exporters, exporter.NewHTTPLogsExporter(ctx, mmdsChan))
	}
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
