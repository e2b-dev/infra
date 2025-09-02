package writer

import (
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// postProcessor prints out "..." 5 seconds after every log
// message, if no other logs have been printed
type postProcessor struct {
	logger   *zap.Logger
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
func (p *postProcessor) run() {
	for {
		select {
		case <-p.done:
			p.ticker.Stop()
			return
		case <-p.ticker.C:
			p.logger.Info("...")
		}
	}
}

func NewPostProcessor(interval time.Duration, core zapcore.Core) (zapcore.Core, func()) {
	pp := &postProcessor{
		logger:   zap.New(core),
		done:     make(chan struct{}),
		interval: interval,
		ticker:   time.NewTicker(interval),
	}

	go pp.run()

	return zapcore.RegisterHooks(core, pp.hook), func() {
		pp.doneOnce.Do(func() {
			pp.done <- struct{}{}
		})
	}
}
