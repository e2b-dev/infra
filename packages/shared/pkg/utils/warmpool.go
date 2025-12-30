package utils

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var meter = otel.Meter("github.com/e2b-dev/infra/packages/shared/pkg/utils")

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

func NewWarmPool[T Item](name, metricPrefix string, maxPoolSize, warmCount int, a ItemFactory[T]) *WarmPool[T] {
	pool := &WarmPool[T]{
		acquisition:   a,
		name:          name,
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

		for item := range wp.freshItems {
			wp.destroy(ctx, item, "done populating")
		}
	}()

	for {
		if err := wp.isClosed(ctx); err != nil {
			return ignoreClosed(err)
		}

		item, err := wp.acquisition.Create(ctx)
		if err != nil {
			wp.createdCounter.Add(ctx, 1, metric.WithAttributes(failureAttr()))
			time.Sleep(time.Second) // wait a bit before retrying

			continue
		}
		wp.createdCounter.Add(ctx, 1, metric.WithAttributes(successAttr()))

		select {
		case <-wp.done:
			wp.beginDestroy(ctx, item, "populate while closed")

			return nil
		case <-ctx.Done():
			wp.beginDestroy(ctx, item, "populate while context canceled")

			return ctx.Err()
		case wp.freshItems <- item:
		}
	}
}

// Get an item from the pool.
// - First try to find a reusable item.
// - If no reusable items are available, return an item from the fresh channel.
// - If the context has been canceled or the `Close` method has been called, return with an error.
func (wp *WarmPool[T]) Get(ctx context.Context) (T, error) {
	var item T

	recordSuccess := func(source string, attrs ...attribute.KeyValue) {
		attrs = append(attrs, successAttr(), sourceAttr(source))
		wp.getCounter.Add(ctx, 1, metric.WithAttributes(attrs...))
	}
	recordFailure := func(reason string, attrs ...attribute.KeyValue) {
		attrs = append(attrs, failureAttr(), reasonAttr(reason))
		wp.getCounter.Add(ctx, 1, metric.WithAttributes(attrs...))
	}

	// early check to bail
	if err := wp.isClosed(ctx); err != nil {
		recordFailure("closed")

		return item, err
	}

	// get from the reusable pool first
	select {
	case <-ctx.Done():
		recordFailure("canceled")

		return item, ctx.Err()
	case <-wp.done:
		recordFailure("closed")

		return item, ErrClosed
	case s, ok := <-wp.reusableItems:
		if !ok {
			recordFailure("reusable closed")

			return item, ErrClosed
		}

		telemetry.ReportEvent(ctx, fmt.Sprintf("reused %s", wp.name))
		recordSuccess("used")

		return s, nil
	default:
	}

	// if that didn't work, get from whichever pool gets an item first
	select {
	case <-ctx.Done():
		recordFailure("canceled")

		return item, ctx.Err()
	case <-wp.done:
		recordFailure("closed")

		return item, ErrClosed
	case s, ok := <-wp.reusableItems:
		if !ok {
			recordFailure("reusable closed")

			return s, ErrClosed
		}

		telemetry.ReportEvent(ctx, fmt.Sprintf("reused %s", wp.name))
		recordSuccess("used")

		return s, nil
	case s, ok := <-wp.freshItems:
		if !ok {
			recordFailure("fresh closed")

			return s, ErrClosed
		}

		telemetry.ReportEvent(ctx, fmt.Sprintf("new %s", wp.name))
		recordSuccess("fresh")

		return s, nil
	}
}

// Return an item to the pool.
// - puts the item in `reusableItems` to get priority when being returned.
// - destroy the item and return if the pool has already been closed.
// - if the item fails to be pushed back into the channel for any reason, destroy it.
func (wp *WarmPool[T]) Return(ctx context.Context, item T) {
	wp.wg.Go(func() {
		recordSuccess := func() {
			wp.returnedCounter.Add(ctx, 1, metric.WithAttributes(
				successAttr()),
			)
		}

		recordFailure := func(reason string) {
			wp.returnedCounter.Add(ctx, 1, metric.WithAttributes(
				failureAttr(), reasonAttr(reason)),
			)
		}

		if err := wp.isClosed(ctx); err != nil {
			recordFailure("closed")

			return
		}

		// if that didn't work, keep trying to push, but listen for failures as well
		select {
		case wp.reusableItems <- item:
			telemetry.ReportEvent(ctx, fmt.Sprintf("returned %s", wp.name))
			recordSuccess()

		case <-ctx.Done():
			wp.beginDestroy(ctx, item, "return failed due to canceled context")
			recordFailure("canceled")

		case <-wp.done:
			wp.beginDestroy(ctx, item, "return failed due to closed pool")
			recordFailure("closed")
		}
	})
}

// Close destroys all items and prevents the pool from creating/returning anymore.
func (wp *WarmPool[T]) Close(ctx context.Context) error {
	logger.L().Info(ctx, "Closing pool")

	var err error

	wp.doneOnce.Do(func() {
		close(wp.done)

		wp.wg.Wait()

		close(wp.reusableItems)

		for item := range wp.reusableItems {
			wp.destroy(ctx, item, "close and destroy reusable items")
		}
	})

	return err
}

func (wp *WarmPool[T]) beginDestroy(ctx context.Context, item T, reason string) {
	wp.wg.Go(func() {
		wp.destroy(ctx, item, reason)
	})
}

func (wp *WarmPool[T]) destroy(ctx context.Context, item T, reason string) {
	ctx = context.WithoutCancel(ctx) // cleanup cannot be canceled

	err := wp.acquisition.Destroy(ctx, item)
	if err != nil {
		wp.destroyedCounter.Add(ctx, 1, metric.WithAttributes(failureAttr(), reasonAttr(reason)))

		logger.L().Error(ctx, "failed to destroy item",
			zap.Error(err),
			zap.String("item", item.String()),
		)

		return
	}

	wp.destroyedCounter.Add(ctx, 1, metric.WithAttributes(successAttr(), reasonAttr(reason)))
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

func failureAttr() attribute.KeyValue {
	return attribute.Bool("success", false)
}

func successAttr() attribute.KeyValue {
	return attribute.Bool("success", true)
}

func sourceAttr(source string) attribute.KeyValue {
	return attribute.String("source", source)
}

func reasonAttr(reason string) attribute.KeyValue {
	return attribute.String("reason", reason)
}

func ignoreClosed(err error) error {
	if errors.Is(err, ErrClosed) {
		return nil
	}

	return err
}
