package network

import (
	"context"
	"errors"
	"fmt"

	"github.com/caarlos0/env/v11"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	NewSlotsPoolSize    = 32
	ReusedSlotsPoolSize = 100
)

type Config struct {
	// Using reserver IPv4 in range that is used for experiments and documentation
	// https://en.wikipedia.org/wiki/Reserved_IP_addresses
	HyperloopIPAddress       string `env:"SANDBOX_HYPERLOOP_IP"         envDefault:"192.0.2.1"`
	HyperloopProxyPort       uint16 `env:"SANDBOX_HYPERLOOP_PROXY_PORT" envDefault:"5010"`
	UseLocalNamespaceStorage bool   `env:"USE_LOCAL_NAMESPACE_STORAGE"`
}

func ParseConfig() (Config, error) {
	return env.ParseAs[Config]()
}

type Pool struct {
	config Config

	done chan struct{}

	newSlots          chan *Slot
	reusedSlots       chan *Slot
	newSlotCounter    metric.Int64UpDownCounter
	reusedSlotCounter metric.Int64UpDownCounter

	slotStorage Storage
}

func NewPool(meterProvider metric.MeterProvider, newSlotsPoolSize, reusedSlotsPoolSize int, nodeID string, config Config) (*Pool, error) {
	newSlots := make(chan *Slot, newSlotsPoolSize-1)
	reusedSlots := make(chan *Slot, reusedSlotsPoolSize)

	meter := meterProvider.Meter("orchestrator.network.pool")

	newSlotCounter, err := telemetry.GetUpDownCounter(meter, telemetry.NewNetworkSlotSPoolCounterMeterName)
	if err != nil {
		return nil, fmt.Errorf("failed to create new slot counter: %w", err)
	}

	reusedSlotsCounter, err := telemetry.GetUpDownCounter(meter, telemetry.ReusedNetworkSlotSPoolCounterMeterName)
	if err != nil {
		return nil, fmt.Errorf("failed to create reused slot counter: %w", err)
	}

	slotStorage, err := NewStorage(vrtSlotsSize, nodeID, config)
	if err != nil {
		return nil, fmt.Errorf("failed to create slot storage: %w", err)
	}

	pool := &Pool{
		config:            config,
		done:              make(chan struct{}),
		newSlots:          newSlots,
		reusedSlots:       reusedSlots,
		newSlotCounter:    newSlotCounter,
		reusedSlotCounter: reusedSlotsCounter,
		slotStorage:       slotStorage,
	}

	return pool, nil
}

func (p *Pool) createNetworkSlot(ctx context.Context) (*Slot, error) {
	ips, err := p.slotStorage.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire network slot: %w", err)
	}

	err = ips.CreateNetwork()
	if err != nil {
		releaseErr := p.slotStorage.Release(ips)
		err = errors.Join(err, releaseErr)

		return nil, fmt.Errorf("failed to create network: %w", err)
	}

	return ips, nil
}

func (p *Pool) Populate(ctx context.Context) error {
	defer close(p.newSlots)

	for {
		select {
		case <-p.done:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		default:
			slot, err := p.createNetworkSlot(ctx)
			if err != nil {
				zap.L().Error("[network slot pool]: failed to create network", zap.Error(err))

				continue
			}

			p.newSlotCounter.Add(ctx, 1)
			p.newSlots <- slot
		}
	}
}

func (p *Pool) Get(ctx context.Context, allowInternet bool) (*Slot, error) {
	var slot *Slot

	select {
	case s := <-p.reusedSlots:
		p.reusedSlotCounter.Add(ctx, -1)
		telemetry.ReportEvent(ctx, "reused network slot")

		slot = s
	default:
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case s := <-p.newSlots:
			p.newSlotCounter.Add(ctx, -1)
			telemetry.ReportEvent(ctx, "new network slot")

			slot = s
		}
	}

	err := slot.ConfigureInternet(ctx, allowInternet)
	if err != nil {
		return nil, fmt.Errorf("error setting slot internet access: %w", err)
	}

	return slot, nil
}

func (p *Pool) Return(ctx context.Context, slot *Slot) error {
	err := slot.ResetInternet(ctx)
	if err != nil {
		// Cleanup the slot if resetting internet fails
		if cerr := p.cleanup(slot); cerr != nil {
			return fmt.Errorf("reset internet: %w; cleanup: %w", err, cerr)
		}

		return fmt.Errorf("error resetting slot internet access: %w", err)
	}

	select {
	case p.reusedSlots <- slot:
		p.reusedSlotCounter.Add(context.Background(), 1) //nolint:contextcheck // TODO: fix this later
	default:
		err := p.cleanup(slot)
		if err != nil {
			return fmt.Errorf("failed to return slot '%d': %w", slot.Idx, err)
		}
	}

	return nil
}

func (p *Pool) cleanup(slot *Slot) error {
	var errs []error

	err := slot.RemoveNetwork()
	if err != nil {
		errs = append(errs, fmt.Errorf("cannot remove network when releasing slot '%d': %w", slot.Idx, err))
	}

	err = p.slotStorage.Release(slot)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to release slot '%d': %w", slot.Idx, err))
	}

	return errors.Join(errs...)
}

func (p *Pool) Close(_ context.Context) error {
	close(p.done)

	zap.L().Info("Closing network pool")

	for slot := range p.newSlots {
		err := p.cleanup(slot)
		if err != nil {
			return fmt.Errorf("failed to cleanup slot '%d': %w", slot.Idx, err)
		}
	}

	close(p.reusedSlots)
	for slot := range p.reusedSlots {
		err := p.cleanup(slot)
		if err != nil {
			return fmt.Errorf("failed to cleanup slot '%d': %w", slot.Idx, err)
		}
	}

	return nil
}
