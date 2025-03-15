package writer

import (
	"go.uber.org/zap"
)

type BuildLogsWriter struct {
	logger *zap.Logger
}

func (w BuildLogsWriter) Write(p []byte) (n int, err error) {
	w.logger.Info(string(p))
	return len(p), nil
}

func New(logger *zap.Logger) BuildLogsWriter {
	writer := BuildLogsWriter{logger: logger}
	return writer
}
