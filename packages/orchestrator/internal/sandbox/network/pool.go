package network

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/caarlos0/env/v11"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	NewSlotsPoolSize    = 32
	ReusedSlotsPoolSize = 100
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

type Config struct {
	// Using reserver IPv4 in range that is used for experiments and documentation
	// https://en.wikipedia.org/wiki/Reserved_IP_addresses
	OrchestratorInSandboxIPAddress string `env:"SANDBOX_ORCHESTRATOR_IP" envDefault:"192.0.2.1"`

	HyperloopProxyPort uint16 `env:"SANDBOX_HYPERLOOP_PROXY_PORT" envDefault:"5010"`
	NFSProxyPort       uint16 `env:"SANDBOX_NFS_PROXY_PORT"       envDefault:"5011"`
	PortmapperPort     uint16 `env:"SANDBOX_PORTMAPPER_PORT"      envDefault:"5012"`

	UseLocalNamespaceStorage bool `env:"USE_LOCAL_NAMESPACE_STORAGE"`

	// TCP firewall ports - separate ports for different traffic types to avoid
	// protocol detection blocking on server-first protocols like SSH.
	// - HTTP port: for traffic destined to port 80 (HTTP Host header inspection)
	// - TLS port: for traffic destined to port 443 (TLS SNI inspection)
	// - Other port: for all other traffic (CIDR-only check, no protocol inspection)
	SandboxTCPFirewallHTTPPort  uint16 `env:"SANDBOX_TCP_FIREWALL_HTTP_PORT"  envDefault:"5016"`
	SandboxTCPFirewallTLSPort   uint16 `env:"SANDBOX_TCP_FIREWALL_TLS_PORT"   envDefault:"5017"`
	SandboxTCPFirewallOtherPort uint16 `env:"SANDBOX_TCP_FIREWALL_OTHER_PORT" envDefault:"5018"`
}

func ParseConfig() (Config, error) {
	return env.ParseAs[Config]()
}

type Pool struct {
	config Config

	done     chan struct{}
	doneOnce sync.Once

	newSlots    chan *Slot
	reusedSlots chan *Slot

	slotStorage Storage
}

var ErrClosed = errors.New("cannot read from a closed pool")

func NewPool(newSlotsPoolSize, reusedSlotsPoolSize int, slotStorage Storage, config Config) *Pool {
	newSlots := make(chan *Slot, newSlotsPoolSize-1)
	reusedSlots := make(chan *Slot, reusedSlotsPoolSize)

	pool := &Pool{
		config:      config,
		done:        make(chan struct{}),
		newSlots:    newSlots,
		reusedSlots: reusedSlots,
		slotStorage: slotStorage,
	}

	return pool
}

func (p *Pool) createNetworkSlot(ctx context.Context) (*Slot, error) {
	ips, err := p.slotStorage.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire network slot: %w", err)
	}

	err = ips.CreateNetwork(ctx)
	if err != nil {
		releaseErr := p.slotStorage.Release(ips)
		err = errors.Join(err, releaseErr)

		return nil, fmt.Errorf("failed to create network: %w", err)
	}

	return ips, nil
}

func (p *Pool) Populate(ctx context.Context) {
	defer close(p.newSlots)

	for {
		select {
		case <-p.done:
			return
		case <-ctx.Done():
			return
		default:
			slot, err := p.createNetworkSlot(ctx)
			if err != nil {
				logger.L().Error(ctx, "[network slot pool]: failed to create network", zap.Error(err))

				continue
			}

			newSlotsAvailableCounter.Add(ctx, 1)
			p.newSlots <- slot
		}
	}
}

func (p *Pool) Get(ctx context.Context, network *orchestrator.SandboxNetworkConfig) (*Slot, error) {
	var slot *Slot

	select {
	case <-p.done:
		return nil, ErrClosed
	case s := <-p.reusedSlots:
		reusableSlotsAvailableCounter.Add(ctx, -1)
		acquiredSlots.Add(ctx, 1, metric.WithAttributes(attribute.String("pool", "reused")))
		telemetry.ReportEvent(ctx, "reused network slot")

		slot = s
	default:
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
	}

	err := slot.ConfigureInternet(ctx, network)
	if err != nil {
		// Return the slot to the pool if configuring internet fails
		go func() {
			if returnErr := p.Return(context.WithoutCancel(ctx), slot); returnErr != nil {
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

	err := slot.ResetInternet(ctx)
	if err != nil {
		// Cleanup the slot if resetting internet fails
		if cerr := p.cleanup(ctx, slot); cerr != nil {
			return fmt.Errorf("reset internet: %w; cleanup: %w", err, cerr)
		}

		return fmt.Errorf("error resetting slot internet access: %w", err)
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return ErrClosed
	case p.reusedSlots <- slot:
		returnedSlotCounter.Add(ctx, 1)
		reusableSlotsAvailableCounter.Add(ctx, 1)
	default:
		err := p.cleanup(ctx, slot)
		if err != nil {
			return fmt.Errorf("failed to return slot '%d': %w", slot.Idx, err)
		}
	}

	return nil
}

func (p *Pool) cleanup(ctx context.Context, slot *Slot) error {
	var errs []error

	err := slot.RemoveNetwork()
	if err != nil {
		errs = append(errs, fmt.Errorf("cannot remove network when releasing slot '%d': %w", slot.Idx, err))
	}

	err = p.slotStorage.Release(slot)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to release slot '%d': %w", slot.Idx, err))
	}

	releasedSlotCounter.Add(ctx, 1)

	return errors.Join(errs...)
}

func (p *Pool) Close(ctx context.Context) error {
	logger.L().Info(ctx, "Closing network pool")

	p.doneOnce.Do(func() {
		close(p.done)
	})

	var errs []error

	for slot := range p.newSlots {
		err := p.cleanup(ctx, slot)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to cleanup slot '%d': %w", slot.Idx, err))
		}
	}

	close(p.reusedSlots)

	for slot := range p.reusedSlots {
		err := p.cleanup(ctx, slot)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to cleanup slot '%d': %w", slot.Idx, err))
		}
	}

	return errors.Join(errs...)
}
