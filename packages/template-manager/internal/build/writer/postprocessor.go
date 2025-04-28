package writer

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

type PostProcessor struct {
	errChan chan error
	ctx     context.Context
	writer  io.Writer

	stopOnce sync.Once
}

// Start starts the post-processing.
func (p *PostProcessor) Start() {
	p.Write("Starting postprocessing")

	for {
		msg := "..."

		select {
		case postprocessingErr := <-p.errChan:
			if postprocessingErr != nil {
				p.Write(fmt.Sprintf("Postprocessing failed: %s", postprocessingErr))
				return
			}

			p.Write(msg)
			p.Write("Postprocessing finished. Cleaning up...")

			return
		case <-p.ctx.Done():
			return
		case <-time.After(5 * time.Second):
			p.Write(msg)
		}
	}

}

func (p *PostProcessor) Stop(err error) {
	p.stopOnce.Do(func() {
		p.errChan <- err
	})
}

func (p *PostProcessor) Write(message string) {
	p.writer.Write([]byte(fmt.Sprintf("[%s] %s\n", time.Now().Format(time.RFC3339), message)))
}

func NewPostProcessor(ctx context.Context, writer io.Writer) *PostProcessor {
	return &PostProcessor{
		ctx:     ctx,
		writer:  writer,
		errChan: make(chan error, 1),
	}
}
