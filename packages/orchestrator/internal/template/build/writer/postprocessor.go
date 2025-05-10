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
}

// Start starts the post-processing.
func (p *PostProcessor) Start() {
	p.WriteMsg("Starting postprocessing")
	startTime := time.Now()

	for {
		msg := "..."

		select {
		case postprocessingErr := <-p.errChan:
			if postprocessingErr != nil {
				p.WriteMsg(fmt.Sprintf("Postprocessing failed: %s", postprocessingErr))
				return
			}

			p.WriteMsg(msg)
			p.WriteMsg(fmt.Sprintf("Postprocessing finished. Took %s ms. Cleaning up...", time.Since(startTime).String()))

			return
		case <-p.ctx.Done():
			return
		case <-p.ticker.C:
			p.WriteMsg(msg)
		}
	}

}

func (p *PostProcessor) Stop(err error) {
	p.stopOnce.Do(func() {
		p.errChan <- err
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
		ticker:  time.NewTicker(tickerInterval),
	}
}

func prefixWithTimestamp(message string) string {
	return fmt.Sprintf("[%s] %s", time.Now().Format(time.RFC3339), message)
}
