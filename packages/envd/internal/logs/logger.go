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

func NewLogger(ctx context.Context, isNotFC bool, mmdsChan <-chan *host.MMDSOpts) *zerolog.Logger {
	zerolog.TimestampFieldName = "timestamp"
	zerolog.TimeFieldFormat = time.RFC3339Nano

	exporters := []io.Writer{}

	var level zerolog.Level
	if isNotFC {
		exporters = append(exporters, os.Stdout)
	} else {
		// HTTP exporter only — stdout goes to journald which dirties guest memory pages.
		exporters = append(exporters, exporter.NewHTTPLogsExporter(ctx, isNotFC, mmdsChan))
	}
	level = zerolog.DebugLevel

	l := zerolog.
		New(io.MultiWriter(exporters...)).
		With().
		Timestamp().
		Logger().
		Level(level)

	return &l
}
