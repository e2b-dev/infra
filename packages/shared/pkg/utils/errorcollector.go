package utils

import (
	"context"
	"errors"
	"sync"

	"golang.org/x/sync/semaphore"
)

// ErrorCollector collects errors from multiple goroutines. This has a similar API to `errgroup.Group`,
// except that it runs all goroutines, and returns all errors; it does not stop after the first error.
type ErrorCollector struct {
	done    chan struct{}
	input   chan error
	errs    []error
	workers *semaphore.Weighted
	wg      sync.WaitGroup
}

// NewErrorCollector creates a new ErrorCollector
func NewErrorCollector(maxConcurrency int) *ErrorCollector {
	done := make(chan struct{})
	input := make(chan error, 10)

	ec := &ErrorCollector{
		done:    done,
		input:   input,
		workers: semaphore.NewWeighted(int64(maxConcurrency)),
	}

	go ec.run()

	return ec
}

func (ec *ErrorCollector) run() {
	for err := range ec.input {
		ec.errs = append(ec.errs, err)
	}

	close(ec.done)
}

func (ec *ErrorCollector) Go(ctx context.Context, fn func() error) {
	ec.wg.Go(func() {
		// limit concurrency
		if err := ec.workers.Acquire(ctx, 1); err != nil {
			ec.input <- err

			return
		}
		defer ec.workers.Release(1)

		err := fn()
		if err != nil {
			ec.input <- err
		}
	})
}

func (ec *ErrorCollector) Wait() error {
	ec.wg.Wait()
	close(ec.input)
	<-ec.done
	switch len(ec.errs) {
	case 0:
		return nil
	case 1:
		return ec.errs[0]
	default:
		return errors.Join(ec.errs...)
	}
}
