package writer

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

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
	type fields struct {
		testErr       error
		shouldContain string
	}
	tests := []struct {
		name   string
		fields fields
	}{
		{
			name: "test error",
			fields: fields{
				testErr:       errors.New("test error"),
				shouldContain: "Build failed:",
			},
		},
		{
			name: "test success",
			fields: fields{
				testErr:       nil,
				shouldContain: "Build finished",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := newTestLogger(&buf)

			ctx := t.Context()
			errChan := make(chan error)

			zap.NewNop()

			p := &PostProcessor{
				Logger:         logger,
				ctx:            ctx,
				errChan:        errChan,
				stopCh:         make(chan struct{}, 1),
				tickerInterval: defaultTickerInterval,
				ticker:         time.NewTicker(defaultTickerInterval),
			}

			end := make(chan struct{}, 1)
			go func() {
				p.Start()

				end <- struct{}{}
			}()
			p.Stop(ctx, tt.fields.testErr)

			// Wait for the start goroutine to finish
			<-end

			logs := buf.String()
			if !strings.Contains(logs, tt.fields.shouldContain) {
				t.Errorf("expected data to contain %s, got %s", tt.fields.shouldContain, logs)
			}
		})
	}
}
