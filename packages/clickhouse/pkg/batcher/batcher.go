package batcher

import (
	"errors"
	"time"
)

const (
	defaultQueueSize    = 8 * 1024
	defaultMaxBatchSize = 64 * 1024
	defaultMaxDelay     = 100 * time.Millisecond
)

var (
	ErrBatcherAlreadyStarted = errors.New("batcher already started")
	ErrBatcherNotStarted     = errors.New("batcher not started")
	ErrFuncNotSet            = errors.New("Batcher.Func must be set")
)

// Batcher groups items in batches and calls Func on them.
//
// See also BytesBatcher.
type Batcher[T any] struct {
	// Func is called by Batcher when batch is ready to be processed.
	Func BatcherFunc[T]

	// Maximum batch size that will be passed to BatcherFunc.
	MaxBatchSize int

	// Maximum delay between Push() and BatcherFunc call.
	MaxDelay time.Duration

	// Maximum unprocessed items' queue size.
	QueueSize int

	// ErrorHandler is called when BatcherFunc returns an error
	// If not set, errors from BatcherFunc will be silently dropped
	// This allows customizing error handling behavior - e.g. logging, metrics, etc.
	ErrorHandler func(error)

	// Synchronization primitives.
	ch     chan T
	doneCh chan struct{}
}

// BatcherFunc is called by Batcher when batch is ready to be processed.
//
// BatcherFunc must process the given batch before returning.
// It must not hold references to the batch after returning.
type BatcherFunc[T any] func(batch []T) error

type BatcherOptions struct {
	// MaxBatchSize is the maximum number of items that will be collected into a single batch
	// before being flushed by the BatcherFunc
	MaxBatchSize int

	// MaxDelay is the maximum time to wait for a batch to fill up before flushing it,
	// even if the batch size hasn't reached MaxBatchSize
	MaxDelay time.Duration

	// QueueSize is the size of the channel buffer used to queue incoming items
	// If the queue is full, new items will be rejected
	QueueSize int

	// ErrorHandler is called when BatcherFunc returns an error
	// If not set, errors from BatcherFunc will be silently dropped
	// This allows customizing error handling behavior - e.g. logging, metrics, etc.
	ErrorHandler func(error)
}

// NewBatcher creates a new Batcher with the given parameters.
func NewBatcher[T any](fn BatcherFunc[T], cfg BatcherOptions) (*Batcher[T], error) {
	b := &Batcher[T]{
		Func:         fn,
		MaxBatchSize: cfg.MaxBatchSize,
		MaxDelay:     cfg.MaxDelay,
		QueueSize:    cfg.QueueSize,
		ErrorHandler: cfg.ErrorHandler,
	}

	if b.Func == nil {
		return nil, ErrFuncNotSet
	}

	if b.ErrorHandler == nil {
		b.ErrorHandler = func(err error) {
			return
		}
	}

	if b.MaxBatchSize <= 0 {
		b.MaxBatchSize = defaultMaxBatchSize
	}
	if b.MaxDelay <= 0 {
		b.MaxDelay = defaultMaxDelay
	}
	if b.QueueSize <= 0 {
		b.QueueSize = defaultQueueSize
	}

	return b, nil
}

// Initialize the synchronization primitives and start batch processing.
func (b *Batcher[T]) Start() error {
	if b.ch != nil {
		return ErrBatcherAlreadyStarted
	}

	b.ch = make(chan T, b.QueueSize)
	b.doneCh = make(chan struct{})

	go func() {
		processBatches(b.Func, b.ch, b.MaxBatchSize, b.MaxDelay, b.ErrorHandler)
		close(b.doneCh)
	}()

	return nil
}

// Stop stops batch processing.
func (b *Batcher[T]) Stop() error {
	if b.ch == nil {
		return ErrBatcherNotStarted
	}
	close(b.ch)
	<-b.doneCh
	b.ch = nil
	b.doneCh = nil
	return nil
}

// Push pushes new batched item into the batcher.
//
// Don't forget calling Start() before pushing items into the batcher.
func (b *Batcher[T]) Push(batchedItem T) (bool, error) {
	if b.ch == nil {
		return false, ErrBatcherNotStarted
	}
	select {
	case b.ch <- batchedItem:
		return true, nil
	default:
		return false, nil
	}
}

// QueueLen returns the number of pending items, which weren't passed into
// BatcherFunc yet.
//
// Maximum number of pending items is Batcher.QueueSize.
func (b *Batcher[T]) QueueLen() int {
	return len(b.ch)
}

func processBatches[T any](f BatcherFunc[T], ch <-chan T, maxBatchedItemBatchSize int, maxBatchedItemDelay time.Duration, errorHandler func(error)) {
	var (
		batch        []T
		batchedItem  T
		ok           bool
		lastPushTime = time.Now()
	)

	for {
		select {
		case batchedItem, ok = <-ch:
			if !ok {
				call(f, batch, errorHandler)
				return
			}
			batch = append(batch, batchedItem)
		default:
			if len(batch) == 0 {
				batchedItem, ok = <-ch
				// Flush what's left in the buffer if the batcher is stopped
				if !ok {
					call(f, batch, errorHandler)
					return
				}
				batch = append(batch, batchedItem)
			} else {
				if delay := maxBatchedItemDelay - time.Since(lastPushTime); delay > 0 {
					t := acquireTimer(delay)
					select {
					case batchedItem, ok = <-ch:
						if !ok {
							call(f, batch, errorHandler)
							return
						}
						batch = append(batch, batchedItem)
					case <-t.C:
					}
					releaseTimer(t)
				}
			}
		}

		if len(batch) >= maxBatchedItemBatchSize || time.Since(lastPushTime) > maxBatchedItemDelay {
			lastPushTime = time.Now()
			call(f, batch, errorHandler)
			batch = batch[:0]
		}
	}
}

func call[T any](f BatcherFunc[T], batch []T, errorHandler func(error)) {
	if len(batch) > 0 {
		err := f(batch)
		if err != nil {
			errorHandler(err)
		}
	}
}
