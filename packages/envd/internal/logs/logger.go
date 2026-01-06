package logs

import (
	"context"
	"io"

	"github.com/rs/zerolog"

	"github.com/e2b-dev/infra/packages/envd/internal/host"
)

func NewLogger(ctx context.Context, isNotFC bool, mmdsChan <-chan *host.MMDSOpts) *zerolog.Logger {
	// Logging disabled for performance - use io.Discard
	l := zerolog.New(io.Discard).Level(zerolog.Disabled)

	return &l
}
