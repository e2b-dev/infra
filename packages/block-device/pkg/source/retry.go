package source

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"
)

type Retrier struct {
	ctx        context.Context
	cancel     context.CancelFunc
	base       io.ReaderAt
	retryDelay time.Duration
	maxRetries int
}

func NewRetrier(ctx context.Context, base io.ReaderAt, maxRetries int, retryDelay time.Duration) *Retrier {
	ctx, cancel := context.WithCancel(ctx)

	return &Retrier{
		ctx:        ctx,
		maxRetries: maxRetries,
		retryDelay: retryDelay,
		base:       base,
		cancel:     cancel,
	}
}

func (r *Retrier) ReadAt(p []byte, off int64) (n int, err error) {
	for i := 0; i < r.maxRetries; i++ {
		select {
		case <-r.ctx.Done():
			return 0, r.ctx.Err()
		default:
			n, err = r.base.ReadAt(p, off)
			if err != nil {
				time.Sleep(r.retryDelay)
				log.Printf("retrying after error: %v\n", err)

				continue
			}

			return n, nil
		}
	}

	return 0, fmt.Errorf("failed to read after %d retries", r.maxRetries)
}

func (r *Retrier) Close() {
	r.cancel()
}
