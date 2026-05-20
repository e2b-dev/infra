package logs

import (
	"time"

	"github.com/rs/zerolog"
)

const (
	defaultMaxBufferSize = 64 << 10 // 64 KiB
	defaultTimeout       = 2 * time.Second
)

func LogBufferedDataEvents(dataCh <-chan []byte, logger *zerolog.Logger, eventType string) {
	timer := time.NewTicker(defaultTimeout)
	defer timer.Stop()

	bytesField := eventType + "_bytes"
	buffer := make([]byte, 0, defaultMaxBufferSize)
	defer func() {
		if len(buffer) > 0 {
			logger.Info().Int(bytesField, len(buffer)).Msg("Streaming process event (flush)")
		}
	}()

	for {
		select {
		case <-timer.C:
			if len(buffer) > 0 {
				logger.Info().Int(bytesField, len(buffer)).Msg("Streaming process event")
				buffer = buffer[:0]
			}
		case data, ok := <-dataCh:
			if !ok {
				return
			}

			buffer = append(buffer, data...)

			if len(buffer) >= defaultMaxBufferSize {
				logger.Info().Int(bytesField, len(buffer)).Msg("Streaming process event")
				buffer = buffer[:0]

				continue
			}
		}
	}
}
