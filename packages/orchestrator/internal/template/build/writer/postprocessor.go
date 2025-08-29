package writer

import (
	"context"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// PostProcessor prints out "..." 5 seconds after every log
// message, if no other logs have been printed
type PostProcessor struct {
	logger *zap.Logger

	ticker   *time.Ticker
	interval time.Duration
}

func (p *PostProcessor) Hook(e zapcore.Entry) error {
	p.ticker.Reset(p.interval)
	return nil
}

// Start the post-processing.
func (p *PostProcessor) run(ctx context.Context) {

	for {
		select {
		case <-ctx.Done():
			p.ticker.Stop()
			return
		case <-p.ticker.C:
			p.logger.Info("...")
		}
	}
}

func NewPostProcessor(ctx context.Context, interval time.Duration, core zapcore.Core) zapcore.Core {
	pp := &PostProcessor{
		logger:   zap.New(core),
		interval: interval,
		ticker:   time.NewTicker(interval),
	}

	go pp.run(ctx)

	return zapcore.RegisterHooks(core, pp.Hook)
}
