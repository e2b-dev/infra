package writer

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

const defaultTickerInterval = 5 * time.Second

type PostProcessor struct {
	tickerInterval time.Duration

	errChan chan error
	ctx     context.Context
	writer  io.Writer
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
				p.WriteMsg(fmt.Sprintf("Postprocessing failed: %s", postprocessingErr))
				return
			} else {
				p.WriteMsg(fmt.Sprintf("Build finished, took %s", time.Since(startTime).Truncate(time.Second).String()))
				return
			}
		case <-p.ctx.Done():
			return
		case <-p.ticker.C:
			p.WriteMsg(msg)
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

func (p *PostProcessor) WriteMsg(message string) {
	p.ticker.Reset(p.tickerInterval)
	p.writer.Write([]byte(prefixWithTimestamp(message + "\n")))
}

func (p *PostProcessor) Write(b []byte) (n int, err error) {
	p.ticker.Reset(p.tickerInterval)
	return p.writer.Write([]byte(prefixWithTimestamp(string(b))))
}

func NewPostProcessor(ctx context.Context, writer io.Writer, enableTicker bool) *PostProcessor {
	// If ticker is not enabled, we use a ticker that ticks way past the build time
	tickerInterval := 24 * time.Hour
	if enableTicker {
		tickerInterval = defaultTickerInterval
	}

	return &PostProcessor{
		ctx:            ctx,
		writer:         writer,
		errChan:        make(chan error, 1),
		stopCh:         make(chan struct{}, 1),
		tickerInterval: tickerInterval,
		ticker:         time.NewTicker(tickerInterval),
	}
}

func prefixWithTimestamp(message string) string {
	return fmt.Sprintf("[%s] %s", time.Now().Format(time.RFC3339), message)
}
