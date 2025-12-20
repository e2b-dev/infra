package utils

import (
	"context"
	"errors"
	"sync"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
)

var (
	meter = otel.Meter("github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network")
)

var ErrClosed = errors.New("cannot read from a closed pool")

type Item interface {
	String() string
}

type ItemFactory[T Item] interface {
	// Create a new item.
	Create(ctx context.Context) (T, error)
	Destroy(ctx context.Context, s T) error
}

type WarmPool[T Item] struct {
	acquisition ItemFactory[T]

	freshItems    chan T
	reusableItems chan T

	name     string
	done     chan struct{}
	doneOnce sync.Once

	// measure pool performance
	getCounter      metric.Int64Counter
	returnedCounter metric.Int64Counter

	// measure factory usage
	createdCounter   metric.Int64Counter
	destroyedCounter metric.Int64Counter

	wg sync.WaitGroup
}

func NewWarmPool[T Item](metricPrefix string, maxPoolSize, warmCount int, a ItemFactory[T]) *WarmPool[T] {
	pool := &WarmPool[T]{
		acquisition:   a,
		freshItems:    make(chan T, warmCount),
		reusableItems: make(chan T, maxPoolSize),
		done:          make(chan struct{}),

		createdCounter: Must(meter.Int64Counter(metricPrefix+".created",
			metric.WithDescription("Number of items created."),
		)),
		destroyedCounter: Must(meter.Int64Counter(metricPrefix+".destroyed",
			metric.WithDescription("Number of items destroyed."),
		)),
		getCounter: Must(meter.Int64Counter(metricPrefix+".retrieved",
			metric.WithDescription("Number of items retrieved."),
		)),
		returnedCounter: Must(meter.Int64Counter(metricPrefix+".returned",
			metric.WithDescription("Number of items returned."),
		)),
	}

	return pool
}

// Populate the pool with fresh items.
// - Keeps the `freshItems` channel full of fresh items
// - Continues to function until the `Close` method has been called, or the context has been cancelled.
// - When done, destroys all items in the `freshItems` channel before returning
// - When an error is encountered, continue trying to create more entries
func (wp *WarmPool[T]) Populate(ctx context.Context) error {
	defer func() {
		close(wp.freshItems)

		for slot := range wp.freshItems {
			wp.destroy(ctx, slot, "done populating")
		}
	}()

	for {
		if err := wp.isClosed(ctx); err != nil {
			return ignoreClosed(err)
		}

		slot, err := wp.acquisition.Create(ctx)
		if err != nil {
			wp.createdCounter.Add(ctx, 1, withFailureAttr())
			continue
		}
		wp.createdCounter.Add(ctx, 1, withSuccessAttr())

		// try to push to the channel first
		select {
		case wp.freshItems <- slot:
			continue
		default:
		}

		// if that didn't work, try to push, but check for done/cancellation
		select {
		case <-wp.done:
			wp.beginDestroy(ctx, slot, "populate while closed")
			return nil
		case <-ctx.Done():
			wp.beginDestroy(ctx, slot, "populate while context canceled")
			return ctx.Err()
		case wp.freshItems <- slot:
		}
	}
}

// Get an item from the pool.
// - First try to find a reusable item.
// - If no reusable items are available, return an item from the fresh channel.
// - If the context has been canceled or the `Close` method has been called, return with an error.
func (wp *WarmPool[T]) Get(ctx context.Context) (i T, e error) {
	var source attribute.KeyValue

	defer func() {
		if e != nil {
			wp.getCounter.Add(ctx, 1, withFailureAttr(source))
		} else {
			wp.getCounter.Add(ctx, 1, withSuccessAttr(source))
		}
	}()

	// early check to bail
	if err := wp.isClosed(ctx); err != nil {
		wp.getCounter.Add(ctx, 1, withFailureAttr())
		return i, err
	}

	// get from the reusable pool first
	select {
	case <-ctx.Done():
		return i, ctx.Err()
	case <-wp.done:
		return i, ErrClosed
	case s := <-wp.reusableItems:
		telemetry.ReportEvent(ctx, "reused network slot")
		source = attribute.String("pool", "used")
		return s, nil
	default:
	}

	// if that didn't work, get from whichever pool gets an item first
	select {
	case <-wp.done:
		return i, ErrClosed
	case <-ctx.Done():
		return i, ctx.Err()
	case s := <-wp.reusableItems:
		telemetry.ReportEvent(ctx, "reused network slot")
		source = attribute.String("pool", "used")
		return s, nil
	case s := <-wp.freshItems:
		source = attribute.String("pool", "fresh")
		telemetry.ReportEvent(ctx, "new network slot")

		return s, nil
	}
}

// Return an item to the pool.
// - puts the item in `reusableItems` to get priority when being returned.
// - destroy the item and return if the pool has already been closed.
// - if the item fails to be pushed back into the channel for any reason, destroy it.
func (wp *WarmPool[T]) Return(ctx context.Context, item T) {
	wp.wg.Go(func() {
		if err := wp.isClosed(ctx); err != nil {
			wp.returnedCounter.Add(ctx, 1, withFailureAttr())
			return
		}

		// try to push item in to reusable pool first
		select {
		case wp.reusableItems <- item:
			telemetry.ReportEvent(ctx, "returned network slot")
			wp.returnedCounter.Add(ctx, 1, withSuccessAttr())
			return
		default:
		}

		// if that didn't work, keep trying to push, but listen for failures as well
		select {
		case wp.reusableItems <- item:
			telemetry.ReportEvent(ctx, "returned network slot")
			wp.returnedCounter.Add(ctx, 1, withSuccessAttr())

		case <-ctx.Done():
			wp.beginDestroy(ctx, item, "return while context canceled")
			wp.returnedCounter.Add(ctx, 1, withFailureAttr())

		case <-wp.done:
			wp.beginDestroy(ctx, item, "return while pool closed")
			wp.returnedCounter.Add(ctx, 1, withFailureAttr())
		}
	})
}

// Close destroys all items and prevents the pool from creating/returning anymore.
func (wp *WarmPool[T]) Close(ctx context.Context) error {
	logger.L().Info(ctx, "Closing network pool")

	if err := wp.isClosed(ctx); err != nil {
		return err
	}

	wp.doneOnce.Do(func() {
		close(wp.done)
		close(wp.reusableItems)
	})

	for slot := range wp.reusableItems {
		wp.destroy(ctx, slot, "close and destroy reusable items")
	}

	wp.wg.Wait()

	return nil
}

func (wp *WarmPool[T]) beginDestroy(ctx context.Context, item T, reason string) {
	wp.wg.Go(func() {
		wp.destroy(ctx, item, reason)
	})
}

func (wp *WarmPool[T]) destroy(ctx context.Context, item T, reason string) {
	ctx = context.WithoutCancel(ctx) // cleanup cannot be canceled

	reasonAttr := attribute.String("reason", reason)

	err := wp.acquisition.Destroy(ctx, item)
	if err != nil {
		wp.destroyedCounter.Add(ctx, 1, withFailureAttr(reasonAttr))

		logger.L().Error(ctx, "failed to destroy item",
			zap.Error(err),
			zap.String("item", item.String()),
		)
		return
	}

	wp.destroyedCounter.Add(ctx, 1, withSuccessAttr(reasonAttr))
}

func (wp *WarmPool[T]) isClosed(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-wp.done:
		return ErrClosed
	default:
		return nil
	}
}

func withFailureAttr(attrs ...attribute.KeyValue) metric.AddOption {
	attrs = append(attrs, attribute.Bool("success", false))
	return metric.WithAttributes(attrs...)
}

func withSuccessAttr(attrs ...attribute.KeyValue) metric.AddOption {
	attrs = append(attrs, attribute.Bool("success", true))
	return metric.WithAttributes(attrs...)
}

func ignoreClosed(err error) error {
	if errors.Is(err, ErrClosed) {
		return nil
	}

	return err
}
