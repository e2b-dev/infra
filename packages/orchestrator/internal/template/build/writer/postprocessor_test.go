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

func newTestLogger(buf *bytes.Buffer) *zap.Logger {
	encoderCfg := zap.NewDevelopmentEncoderConfig()
	encoder := zapcore.NewConsoleEncoder(encoderCfg)

	core := zapcore.NewCore(
		encoder,
		zapcore.AddSync(buf),
		zapcore.DebugLevel,
	)

	return zap.New(core)
}

func TestPostProcessor_Start(t *testing.T) {
	var buf bytes.Buffer
	logger := newTestLogger(&buf)

	ctx := t.Context()
	ctx, cancel := context.WithCancel(ctx)

	p := PostProcessor{
		Logger:         logger,
		ticker:         time.NewTicker(time.Second),
		tickerInterval: time.Second,
	}

	// we control the invocation of `start` so we can
	// verify that context cancellation works
	end := make(chan struct{}, 1)
	go func() {
		p.start(ctx)
		end <- struct{}{}
	}()

	// log some info
	p.Info("info message")
	p.Error("error message")

	// stop the post processor
	cancel()

	// Wait for the start goroutine to finish
	<-end

	logs := buf.String()
	assert.Contains(t, logs, "info message")
	assert.Contains(t, logs, "error message")
}
