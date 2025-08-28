package writer

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func newTestCore(buf *bytes.Buffer) zapcore.Core {
	encoderCfg := zap.NewDevelopmentEncoderConfig()
	encoderCfg.TimeKey = ""
	encoder := zapcore.NewConsoleEncoder(encoderCfg)

	core := zapcore.NewCore(
		encoder,
		zapcore.AddSync(buf),
		zapcore.DebugLevel,
	)

	return core
}

func TestPostProcessor_Start(t *testing.T) {
	var buf bytes.Buffer
	core := newTestCore(&buf)

	ctx := t.Context()
	ctx, cancel := context.WithCancel(ctx)

	interval := time.Millisecond * 100
	halfInterval := time.Duration(float64(interval) * 0.5)

	core = NewPostProcessor(ctx, interval, core)
	logger := zap.New(core)

	// log some info
	logger.Info("info message")
	time.Sleep(halfInterval)
	logger.Error("error message")
	time.Sleep(interval + halfInterval)
	logger.Warn("warn message")
	time.Sleep(interval + interval + halfInterval)

	// stop the post processor
	cancel()

	logger.Info("test is complete")

	logs := buf.String()
	assert.Equal(t, logs, `INFO	info message
ERROR	error message
INFO	...
WARN	warn message
INFO	...
INFO	...
INFO	test is complete
`)
}
