package network

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type networkSlotFactory struct {
	slotStorage Storage
}

var _ utils.ItemFactory[*Slot] = (*networkSlotFactory)(nil)

func (n *networkSlotFactory) Create(ctx context.Context) (*Slot, error) {
	ips, err := n.slotStorage.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire network slot: %w", err)
	}

	err = ips.CreateNetwork(ctx)
	if err != nil {
		releaseErr := n.slotStorage.Release(ips)
		err = errors.Join(err, releaseErr)

		return nil, fmt.Errorf("failed to create network: %w", err)
	}

	return ips, nil
}

func (n *networkSlotFactory) Destroy(_ context.Context, slot *Slot) error {
	var errs []error

	err := slot.RemoveNetwork()
	if err != nil {
		errs = append(errs, fmt.Errorf("cannot remove network when releasing slot '%d': %w", slot.Idx, err))
	}

	err = n.slotStorage.Release(slot)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to release slot '%d': %w", slot.Idx, err))
	}

	return errors.Join(errs...)
}

type Pool struct {
	wp      *utils.WarmPool[*Slot]
	factory *networkSlotFactory
}

func NewPool(slotStorage Storage, config Config) *Pool {
	factory := &networkSlotFactory{slotStorage: slotStorage}

	pool := &Pool{
		wp: utils.NewWarmPool[*Slot](
			"network slot",
			"orchestrator.network.slots_pool",
			config.NetworkSlotsReusePoolSize,
			config.NetworkSlotsFreshPoolSize,
			factory,
		),
		factory: factory,
	}

	return pool
}

func (p *Pool) Populate(ctx context.Context) {
	if err := p.wp.Populate(ctx); err != nil {
		logger.L().Error(ctx, "failed to populate network pool", zap.Error(err))
	}
}

func (p *Pool) destroyBadSlot(ctx context.Context, slot *Slot, operation string) {
	// destroy the namespace, assume it's bad
	if err := p.factory.Destroy(context.WithoutCancel(ctx), slot); err != nil {
		logger.L().Error(ctx, "failed to destroy slot",
			zap.String("operation", operation),
			zap.Int("slot_index", slot.Idx),
			zap.Error(err),
		)
	}
}

func (p *Pool) Get(ctx context.Context, network *orchestrator.SandboxNetworkConfig) (*Slot, error) {
	slot, err := p.wp.Get(ctx)
	if err != nil {
		return nil, err
	}

	err = slot.ConfigureInternet(ctx, network)
	if err != nil {
		go p.destroyBadSlot(context.WithoutCancel(ctx), slot, "setting internet access")

		return nil, fmt.Errorf("error setting slot internet access: %w", err)
	}

	return slot, nil
}

func (p *Pool) Return(ctx context.Context, slot *Slot) error {
	err := slot.ResetInternet(ctx)
	if err != nil {
		go p.destroyBadSlot(context.WithoutCancel(ctx), slot, "resetting internet access")

		return fmt.Errorf("error resetting slot internet access: %w", err)
	}

	p.wp.Return(ctx, slot)

	return nil
}

func (p *Pool) Close(ctx context.Context) error {
	return p.wp.Close(ctx)
}
