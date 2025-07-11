package writer

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

const tickerInterval = 5 * time.Second

type PostProcessor struct {
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
	p.WriteMsg("Starting postprocessing")
	startTime := time.Now()

	for {
		msg := "..."

		select {
		case postprocessingErr := <-p.errChan:
			p.WriteMsg(msg)

			if postprocessingErr != nil {
				p.WriteMsg(fmt.Sprintf("Postprocessing failed: %s", postprocessingErr))
				return
			}
			p.WriteMsg(fmt.Sprintf("Postprocessing finished. Took %s. Cleaning up...", time.Since(startTime).Truncate(time.Second).String()))

			return
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
	p.ticker.Reset(tickerInterval)
	p.writer.Write([]byte(prefixWithTimestamp(message + "\n")))
}

func (p *PostProcessor) Write(b []byte) (n int, err error) {
	p.ticker.Reset(tickerInterval)
	return p.writer.Write([]byte(prefixWithTimestamp(string(b))))
}

func NewPostProcessor(ctx context.Context, writer io.Writer) *PostProcessor {
	return &PostProcessor{
		ctx:     ctx,
		writer:  writer,
		errChan: make(chan error, 1),
		stopCh:  make(chan struct{}, 1),
		ticker:  time.NewTicker(tickerInterval),
	}
}

func prefixWithTimestamp(message string) string {
	return fmt.Sprintf("[%s] %s", time.Now().Format(time.RFC3339), message)
}
