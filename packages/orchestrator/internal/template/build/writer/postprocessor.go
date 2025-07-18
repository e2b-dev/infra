package writer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const defaultTickerInterval = 5 * time.Second

type PostProcessor struct {
	*zap.Logger

	tickerInterval time.Duration

	errChan chan error
	ctx     context.Context
	ticker  *time.Ticker

	stopOnce sync.Once
	stopCh   chan struct{}
}

// Start starts the post-processing.
func (p *PostProcessor) Start() {
	defer func() {
		// Signal the stop channel to indicate that postprocessing is done
		p.stopCh <- struct{}{}
	}()
	startTime := time.Now()

	for {
		msg := "..."

		select {
		case postprocessingErr := <-p.errChan:
			if postprocessingErr != nil {
				p.Error(fmt.Sprintf("Build failed: %s", postprocessingErr))
			} else {
				p.Info(fmt.Sprintf("Build finished, took %s", time.Since(startTime).Truncate(time.Second).String()))
			}
			return
		case <-p.ctx.Done():
			return
		case <-p.ticker.C:
			p.Info(msg)
		}
	}
}

func (p *PostProcessor) Stop(ctx context.Context, err error) {
	p.stopOnce.Do(func() {
		p.errChan <- err

		// Wait for the postprocessing to finish
		select {
		case <-p.stopCh:
			return
		case <-ctx.Done():
			return
		}
	})
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

	return &PostProcessor{
		Logger:         writer,
		ctx:            ctx,
		errChan:        make(chan error, 1),
		stopCh:         make(chan struct{}, 1),
		tickerInterval: tickerInterval,
		ticker:         time.NewTicker(tickerInterval),
	}
}
