package network

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var (
	meter = otel.Meter("github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network")

	newSlotsAvailableCounter = utils.Must(meter.Int64UpDownCounter("orchestrator.network.slots_pool.new",
		metric.WithDescription("Number of new network slots ready to be used."),
		metric.WithUnit("{slot"),
	))
	reusableSlotsAvailableCounter = utils.Must(meter.Int64UpDownCounter("orchestrator.network.slots_pool.reused",
		metric.WithDescription("Number of reused network slots ready to be used."),
		metric.WithUnit("{slot}"),
	))
	acquiredSlots = utils.Must(meter.Int64Counter("orchestrator.network.slots_pool.acquired",
		metric.WithDescription("Number of network slots acquired."),
		metric.WithUnit("{slot}"),
	))
	returnedSlotCounter = utils.Must(meter.Int64Counter("orchestrator.network.slots_pool.returned",
		metric.WithDescription("Number of network slots returned."),
		metric.WithUnit("{slot}"),
	))
	releasedSlotCounter = utils.Must(meter.Int64Counter("orchestrator.network.slots_pool.released",
		metric.WithDescription("Number of network slots released."),
		metric.WithUnit("{slot}"),
	))
)

type Pool struct {
	config Config

	done     chan struct{}
	doneOnce sync.Once

	newSlots    chan *Slot
	reusedSlots chan *Slot

	slotStorage Storage
	operations  Operations
}

var ErrClosed = errors.New("cannot read from a closed pool")

func NewPool(operations Operations, slotStorage Storage, config Config) *Pool {
	newSlots := make(chan *Slot, config.NetworkSlotsFreshPoolSize)
	reusedSlots := make(chan *Slot, config.NetworkSlotsReusePoolSize)

	pool := &Pool{
		config:      config,
		done:        make(chan struct{}),
		newSlots:    newSlots,
		reusedSlots: reusedSlots,
		slotStorage: slotStorage,
		operations:  operations,
	}

	return pool
}

func (p *Pool) createNetworkSlot(ctx context.Context) (*Slot, error) {
	ips, err := p.slotStorage.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire network slot: %w", err)
	}

	err = p.operations.CreateNetwork(ctx, ips)
	if err != nil {
		releaseErr := p.slotStorage.Release(ctx, ips)
		err = errors.Join(err, releaseErr)

		return nil, fmt.Errorf("failed to create network: %w", err)
	}

	return ips, nil
}

func (p *Pool) Populate(ctx context.Context) error {
	defer func() {
		close(p.newSlots)

		for slot := range p.newSlots {
			p.cleanup(ctx, slot)
		}
	}()

	for {
		if err := p.isClosed(ctx); err != nil {
			return ignoreClosed(err)
		}

		slot, err := p.createNetworkSlot(ctx)
		if err != nil {
			logger.L().Error(ctx, "[network slot pool]: failed to create network", zap.Error(err))

			continue
		}

		select {
		case <-p.done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case p.newSlots <- slot:
			newSlotsAvailableCounter.Add(ctx, 1)
		}
	}
}

func (p *Pool) Get(ctx context.Context, network *orchestrator.SandboxNetworkConfig) (*Slot, error) {
	if err := p.isClosed(ctx); err != nil {
		return nil, err
	}

	var slot *Slot

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-p.done:
		return nil, ErrClosed
	case s := <-p.reusedSlots:
		reusableSlotsAvailableCounter.Add(ctx, -1)
		acquiredSlots.Add(ctx, 1, metric.WithAttributes(attribute.String("pool", "reused")))
		telemetry.ReportEvent(ctx, "reused network slot")

		slot = s
	default:
	}

	select {
	case <-p.done:
		return nil, ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	case s := <-p.newSlots:
		newSlotsAvailableCounter.Add(ctx, -1)
		acquiredSlots.Add(ctx, 1, metric.WithAttributes(attribute.String("pool", "new")))
		telemetry.ReportEvent(ctx, "new network slot")

		slot = s
	}

	err := p.operations.ConfigureInternet(ctx, slot, network)
	if err != nil {
		// Return the slot to the pool if configuring internet fails
		go func() {
			if returnErr := p.Return(ctx, slot); returnErr != nil {
				logger.L().Error(ctx, "failed to return slot to the pool", zap.Error(returnErr), zap.Int("slot_index", slot.Idx))
			}
		}()

		return nil, fmt.Errorf("error setting slot internet access: %w", err)
	}

	return slot, nil
}

func (p *Pool) Return(ctx context.Context, slot *Slot) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return ErrClosed
	default:
	}

	err := p.operations.ResetInternet(ctx, slot)
	if err != nil {
		// Cleanup the slot if resetting internet fails
		go p.cleanup(ctx, slot)

		return fmt.Errorf("error resetting slot internet access: %w", err)
	}

	select {
	case <-ctx.Done():
		go p.cleanup(ctx, slot)

		return ctx.Err()
	case <-p.done:
		go p.cleanup(ctx, slot)

		return ErrClosed
	case p.reusedSlots <- slot:
		returnedSlotCounter.Add(ctx, 1)
		reusableSlotsAvailableCounter.Add(ctx, 1)
	default:
		// reused slots was full, drop the slot
		go p.cleanup(ctx, slot)
	}

	return nil
}

func (p *Pool) cleanup(ctx context.Context, slot *Slot) {
	var errs []error

	err := p.operations.RemoveNetwork(ctx, slot)
	if err != nil {
		errs = append(errs, fmt.Errorf("cannot remove network when releasing slot '%d': %w", slot.Idx, err))
	}

	err = p.slotStorage.Release(ctx, slot)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to release slot '%d': %w", slot.Idx, err))
	}

	releasedSlotCounter.Add(ctx, 1)

	if err = errors.Join(errs...); err != nil {
		logger.L().Error(ctx, "failed to cleanup slot",
			zap.Error(err),
			zap.Int("slot_index", slot.Idx),
		)
	}
}

func (p *Pool) Close(ctx context.Context) error {
	logger.L().Info(ctx, "Closing network pool")

	if err := p.isClosed(ctx); err != nil {
		return err
	}

	p.doneOnce.Do(func() {
		close(p.done)
		close(p.reusedSlots)
	})

	for slot := range p.reusedSlots {
		p.cleanup(ctx, slot)
	}

	return nil
}

func (p *Pool) isClosed(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return ErrClosed
	default:
		return nil
	}
}

func ignoreClosed(err error) error {
	if errors.Is(err, ErrClosed) {
		return nil
	}

	return err
}
