package utils

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	defaultReturnTimeout        = 5 * time.Minute
	defaultSleepOnCreateFailure = 1 * time.Second
)

var ErrPoolClosed = errors.New("cannot read from a closed pool")

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

	returnTimeout      time.Duration
	sleepOnCreateError time.Duration
	name               string
	done               chan struct{}
	doneOnce           sync.Once

	// measure pool usage
	operationsMetric metric.Int64Histogram

	wg sync.WaitGroup
}

func NewWarmPool[T Item](
	name, metricPrefix string,
	maxPoolSize, warmCount int,
	a ItemFactory[T],
) *WarmPool[T] {
	pool := &WarmPool[T]{
		acquisition:        a,
		name:               name,
		returnTimeout:      defaultReturnTimeout,
		sleepOnCreateError: defaultSleepOnCreateFailure,

		freshItems:    make(chan T, warmCount),
		reusableItems: make(chan T, maxPoolSize),
		done:          make(chan struct{}),

		operationsMetric: Must(meter.Int64Histogram(metricPrefix+".operations",
			metric.WithDescription("Number of operations performed."),
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
	for {
		if _, err := wp.isClosed(ctx); err != nil {
			return ignoreClosed(err)
		}

		item, err := wp.create(ctx)
		if err != nil {
			logger.L().Error(ctx, "failed to create item", zap.Error(err))
			time.Sleep(wp.sleepOnCreateError) // wait a bit before retrying

			continue
		}

		select {
		case <-wp.done:
			wp.destroyAsync(ctx, item, operationPopulate, reasonPoolClosed)

			return nil
		case <-ctx.Done():
			wp.destroyAsync(ctx, item, operationPopulate, reasonContextDone)

			return ctx.Err()
		case wp.freshItems <- item:
		}
	}
}

func (wp *WarmPool[T]) create(ctx context.Context) (T, error) {
	timer := wp.startTimer(operationCreate)
	defer timer.stop(ctx)

	item, err := wp.acquisition.Create(ctx)
	if err != nil {
		timer.failure()

		return item, err
	}

	timer.success()

	return item, nil
}

// Get an item from the pool.
// - First try to find a reusable item.
// - If no reusable items are available, return an item from the fresh channel.
// - If the context has been canceled or the `Close` method has been called, return with an error.
func (wp *WarmPool[T]) Get(ctx context.Context) (T, error) {
	timer := wp.startTimer(operationGet)
	defer timer.stop(ctx)

	var item T

	// early check to bail
	if reason, err := wp.isClosed(ctx); err != nil {
		timer.failure(reason)

		return item, err
	}

	// get from the reusable pool first
	select {
	case <-ctx.Done():
		timer.failure(reasonContextDone)

		return item, ctx.Err()
	case <-wp.done:
		timer.failure(reasonPoolClosed)

		return item, ErrPoolClosed
	case s, ok := <-wp.reusableItems:
		if !ok {
			timer.failure(reasonReusableClosed)

			return item, ErrPoolClosed
		}

		telemetry.ReportEvent(ctx, fmt.Sprintf("reused %s", wp.name))
		timer.success(sourceReuse)

		return s, nil
	default:
	}

	// if that didn't work, get from whichever pool gets an item first
	select {
	case <-ctx.Done():
		timer.failure(reasonContextDone)

		return item, ctx.Err()
	case <-wp.done:
		timer.failure(reasonPoolClosed)

		return item, ErrPoolClosed
	case s, ok := <-wp.reusableItems:
		if !ok {
			timer.failure(reasonReusableClosed)

			return s, ErrPoolClosed
		}

		telemetry.ReportEvent(ctx, fmt.Sprintf("reused %s", wp.name))
		timer.success(sourceReuse)

		return s, nil
	case s, ok := <-wp.freshItems:
		if !ok {
			timer.failure(reasonFreshClosed)

			return s, ErrPoolClosed
		}

		telemetry.ReportEvent(ctx, fmt.Sprintf("new %s", wp.name))
		timer.success(sourceFresh)

		return s, nil
	}
}

// Return an item to the pool.
// - puts the item in `reusableItems` to get priority when being returned.
// - destroy the item and return if the pool has already been closed.
// - if the item fails to be pushed back into the channel for any reason, destroy it.
func (wp *WarmPool[T]) Return(ctx context.Context, item T) {
	timer := wp.startTimer(operationReturn)

	if reason, err := wp.isClosed(ctx); err != nil {
		timer.failure(reason)
		timer.stop(ctx)

		return
	}

	wp.wg.Go(func() {
		defer timer.stop(ctx)

		if reason, err := wp.isClosed(ctx); err != nil {
			timer.failure(reason)

			return
		}

		// if that didn't work, keep trying to push, but listen for failures as well
		select {
		case wp.reusableItems <- item:
			telemetry.ReportEvent(ctx, fmt.Sprintf("returned %s", wp.name))
			timer.success()

		case <-ctx.Done():
			wp.destroyAsync(ctx, item, operationReturn, reasonContextDone)
			timer.failure(reasonContextDone)

		case <-wp.done:
			wp.destroyAsync(ctx, item, operationReturn, reasonPoolClosed)
			timer.failure(reasonPoolClosed)
		case <-time.After(wp.returnTimeout):
			wp.destroyAsync(ctx, item, operationReturn, reasonReturnTimeout)
			timer.failure(reasonReturnTimeout)
		}
	})
}

// Close destroys all items and prevents the pool from creating/returning anymore.
func (wp *WarmPool[T]) Close(ctx context.Context) error {
	logger.L().Info(ctx, "Closing pool")
	
	var err error

	wp.doneOnce.Do(func() {
		close(wp.done)

		close(wp.reusableItems)

		for item := range wp.reusableItems {
			wp.destroy(ctx, item, operationClose, reasonCleanupReusable)
		}

		close(wp.freshItems)

		// closing this channel is done in Populate
		for item := range wp.freshItems {
			wp.destroy(ctx, item, operationClose, reasonCleanupFresh)
		}

		wp.wg.Wait()
	})

	return err
}

func (wp *WarmPool[T]) destroyAsync(ctx context.Context, item T, operation operationType, reason failureReasonType) {
	wp.wg.Go(func() {
		wp.destroy(ctx, item, operation, reason)
	})
}

func (wp *WarmPool[T]) destroy(ctx context.Context, item T, trigger operationType, destroyReason failureReasonType) {
	ctx = context.WithoutCancel(ctx) // cleanup cannot be canceled

	timer := wp.startTimer(operationDestroy,
		withAttr("trigger_op", trigger),
		withAttr("trigger_reason", destroyReason),
	)
	defer timer.stop(ctx)

	err := wp.acquisition.Destroy(ctx, item)
	if err != nil {
		timer.failure()

		logger.L().Error(ctx, "failed to destroy item",
			zap.Error(err),
			zap.String("item", item.String()),
		)

		return
	}

	timer.success()
}

func (wp *WarmPool[T]) isClosed(ctx context.Context) (failureReasonType, error) {
	select {
	case <-ctx.Done():
		return reasonContextDone, ctx.Err()
	case <-wp.done:
		return reasonPoolClosed, ErrPoolClosed
	default:
		return "", nil
	}
}

func ignoreClosed(err error) error {
	if errors.Is(err, ErrPoolClosed) {
		return nil
	}

	return err
}
