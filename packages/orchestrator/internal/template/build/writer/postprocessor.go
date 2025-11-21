package writer

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// postProcessor prints out "..." 5 seconds after every log
// message, if no other logs have been printed
type postProcessor struct {
	logger   logger.Logger
	done     chan struct{}
	doneOnce sync.Once
	ticker   *time.Ticker
	interval time.Duration
}

func (p *postProcessor) hook(_ zapcore.Entry) error {
	p.ticker.Reset(p.interval)

	return nil
}

// Start the post-processing.
func (p *postProcessor) run(ctx context.Context) {
	for {
		select {
		case <-p.done:
			p.ticker.Stop()

			return
		case <-p.ticker.C:
			p.logger.Info(ctx, "...")
		}
	}
}

func NewPostProcessor(ctx context.Context, interval time.Duration, core zapcore.Core) (zapcore.Core, func()) {
	pp := &postProcessor{
		logger:   logger.NewTracedLoggerFromCore(core),
		done:     make(chan struct{}),
		interval: interval,
		ticker:   time.NewTicker(interval),
	}

	go pp.run(ctx)

	return zapcore.RegisterHooks(core, pp.hook), func() {
		pp.doneOnce.Do(func() {
			pp.done <- struct{}{}
		})
	}
}
