package writer

import (
	"bytes"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// syncBuffer wraps bytes.Buffer with a mutex for concurrent writes from
// the PostProcessor goroutine and the test goroutine.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) Sync() error { return nil }

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

func newTestCore(buf *syncBuffer) zapcore.Core {
	encoderCfg := zap.NewDevelopmentEncoderConfig()
	encoderCfg.TimeKey = ""
	encoder := zapcore.NewConsoleEncoder(encoderCfg)

	core := zapcore.NewCore(
		encoder,
		buf,
		zapcore.DebugLevel,
	)

	return core
}

func TestPostProcessor_Start(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	var buf syncBuffer
	core := newTestCore(&buf)

	interval := time.Millisecond * 100
	halfInterval := time.Duration(float64(interval) * 0.5)

	core, done := NewPostProcessor(ctx, interval, core)
	l := logger.NewTracedLoggerFromCore(core)

	// log some info
	l.Info(ctx, "info message")
	time.Sleep(halfInterval)
	l.Error(ctx, "error message")
	time.Sleep(interval + halfInterval)
	l.Warn(ctx, "warn message")
	time.Sleep(interval + interval + halfInterval)

	// stop the post processor
	done()

	l.Info(ctx, "test is complete")

	logs := buf.String()
	assert.Equal(t, `INFO	info message
ERROR	error message
INFO	...
WARN	warn message
INFO	...
INFO	...
INFO	test is complete
`, logs)
}
