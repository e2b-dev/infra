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

	resetCh chan struct{}
}

func (p *PostProcessor) Hook(e zapcore.Entry) error {
	if p.resetCh == nil {
		return nil
	}

	p.resetCh <- struct{}{}
	return nil
}

// Start the post-processing.
func (p *PostProcessor) run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)

	for {
		select {
		case <-ctx.Done():
			ch := p.resetCh
			p.resetCh = nil
			close(ch)
			return
		case <-t.C:
			p.logger.Info("...")
		case <-p.resetCh:
			t.Reset(interval)
		}
	}
}

func NewPostProcessor(ctx context.Context, interval time.Duration, core zapcore.Core) zapcore.Core {
	pp := &PostProcessor{
		logger:  zap.New(core),
		resetCh: make(chan struct{}, 5),
	}

	go pp.run(ctx, interval)

	return zapcore.RegisterHooks(core, pp.Hook)
}
