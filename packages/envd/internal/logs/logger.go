package logs

import (
	"io"
	"os"
	"time"

	"github.com/rs/zerolog"
)

func NewLogger(verbose bool, writers ...io.Writer) *zerolog.Logger {
	zerolog.TimestampFieldName = "timestamp"
	zerolog.TimeFieldFormat = time.RFC3339Nano

	if verbose {
		writers = append(writers, os.Stdout)
	}

	l := zerolog.
		New(io.MultiWriter(writers...)).
		With().
		Timestamp().
		Logger().
		Level(zerolog.DebugLevel)

	return &l
}
