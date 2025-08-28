package writer

import (
	"context"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const defaultTickerInterval = 5 * time.Second

type PostProcessor struct {
	*zap.Logger

	tickerInterval time.Duration

	ticker *time.Ticker
}

// Start the post-processing.
func (p *PostProcessor) start(ctx context.Context) {
	for {
		msg := "..."

		select {
		case <-ctx.Done():
			return
		case <-p.ticker.C:
			p.Info(msg)
		}
	}
}

func (p *PostProcessor) Log(lvl zapcore.Level, msg string, fields ...zap.Field) {
	p.ticker.Reset(p.tickerInterval)
	p.Logger.Log(lvl, msg, fields...)
}

func (p *PostProcessor) Debug(msg string, fields ...zap.Field) {
	p.Log(zapcore.DebugLevel, msg, fields...)
}

func (p *PostProcessor) Info(msg string, fields ...zap.Field) {
	p.Log(zapcore.InfoLevel, msg, fields...)
}

func (p *PostProcessor) Error(msg string, fields ...zap.Field) {
	p.Log(zapcore.ErrorLevel, msg, fields...)
}

func NewPostProcessor(ctx context.Context, writer *zap.Logger, enableTicker bool) *PostProcessor {
	// If ticker is not enabled, we use a ticker that ticks way past the build time
	tickerInterval := 24 * time.Hour
	if enableTicker {
		tickerInterval = defaultTickerInterval
	}

	pp := &PostProcessor{
		Logger:         writer,
		tickerInterval: tickerInterval,
		ticker:         time.NewTicker(tickerInterval),
	}

	go pp.start(ctx)

	return pp
}
