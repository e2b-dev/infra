package network

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

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

	// ReturnDelay is how long we wait before returning a slot to the reused pool,
	// to let inflight requests on the previous sandbox drain and reduce reuse churn.
	ReturnDelay = 3 * time.Second
)

var (
	meter = otel.Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network")

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

type ReleaseNotify func(ctx context.Context, ip string)

type Config struct {
	// Using reserver IPv4 in range that is used for experiments and documentation
	// https://en.wikipedia.org/wiki/Reserved_IP_addresses
	OrchestratorInSandboxIPAddress string `env:"SANDBOX_ORCHESTRATOR_IP" envDefault:"192.0.2.1"`

	HyperloopProxyPort uint16 `env:"SANDBOX_HYPERLOOP_PROXY_PORT" envDefault:"5010"`
	NFSProxyPort       uint16 `env:"SANDBOX_NFS_PROXY_PORT"       envDefault:"5011"`
	PortmapperPort     uint16 `env:"SANDBOX_PORTMAPPER_PORT"      envDefault:"5012"`

	UseLocalNamespaceStorage bool `env:"USE_LOCAL_NAMESPACE_STORAGE"`

	// Comma-separated CIDRs to allow through the predefined firewall deny list.
	// These are allowed before the private-range deny rules, so they can
	// reach hosts in the 10.0.0.0/8, 172.16.0.0/12, etc. blocks.
	AllowSandboxInternalCIDRs []string `env:"ALLOW_SANDBOX_INTERNAL_CIDRS" envDefault:"" envSeparator:","`

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

	closeMu sync.RWMutex
	closed  bool

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
			if returnErr := p.recycle(context.WithoutCancel(ctx), slot); returnErr != nil {
				logger.L().Error(ctx, "failed to return slot to the pool", zap.Error(returnErr), zap.Int("slot_index", slot.Idx))
			}
		}()

		return nil, fmt.Errorf("error setting slot internet access: %w", err)
	}

	return slot, nil
}

// Return recycles a slot that was used by a sandbox. It waits returnDelay
// before making the slot reusable to let inflight requests on the previous
// sandbox drain.
func (p *Pool) Return(ctx context.Context, slot *Slot, releasedFn ReleaseNotify, returnDelay time.Duration) error {
	notifyNetworkRelease := sync.OnceFunc(func() {
		releasedFn(ctx, slot.HostIPString())
	})
	// Make sure we notify for all code paths
	defer notifyNetworkRelease()

	// If the pool is closed or the context is cancelled during the delay we
	// still fall through and clean up the slot to avoid leaking it.
	select {
	case <-ctx.Done():
		if cerr := p.cleanup(ctx, slot); cerr != nil {
			return errors.Join(ctx.Err(), fmt.Errorf("cleanup slot '%d' on cancelled context: %w", slot.Idx, cerr))
		}

		return ctx.Err()
	case <-p.done:
		if cerr := p.cleanup(ctx, slot); cerr != nil {
			return errors.Join(ErrClosed, fmt.Errorf("cleanup slot '%d' on closed pool: %w", slot.Idx, cerr))
		}

		return ErrClosed
	case <-time.After(returnDelay):
	}

	// Notify right before the release
	notifyNetworkRelease()

	return p.recycle(ctx, slot)
}

// recycle resets the slot's internet configuration and puts it back into the
// reused pool, or cleans it up if the pool is full or closed.
func (p *Pool) recycle(ctx context.Context, slot *Slot) error {
	err := slot.ResetInternet(ctx)
	if err != nil {
		if cerr := p.cleanup(ctx, slot); cerr != nil {
			return fmt.Errorf("reset internet: %w; cleanup: %w", err, cerr)
		}

		return fmt.Errorf("error resetting slot internet access: %w", err)
	}

	// RLock only guards the closed flag and the reusedSlots send. It is
	// released before cleanup() so Close()'s Lock() is never pinned by a
	// slow RemoveNetwork syscall (iptables, netlink) running in a
	// concurrent Return.
	p.closeMu.RLock()

	if p.closed {
		p.closeMu.RUnlock()

		if cerr := p.cleanup(ctx, slot); cerr != nil {
			return errors.Join(ErrClosed, fmt.Errorf("cleanup slot '%d' on closed pool: %w", slot.Idx, cerr))
		}

		return ErrClosed
	}

	select {
	case <-ctx.Done():
		p.closeMu.RUnlock()

		if cerr := p.cleanup(ctx, slot); cerr != nil {
			return errors.Join(ctx.Err(), fmt.Errorf("cleanup slot '%d' on cancelled context: %w", slot.Idx, cerr))
		}

		return ctx.Err()
	case <-p.done:
		p.closeMu.RUnlock()

		if cerr := p.cleanup(ctx, slot); cerr != nil {
			return errors.Join(ErrClosed, fmt.Errorf("cleanup slot '%d' on closing pool: %w", slot.Idx, cerr))
		}

		return ErrClosed
	case p.reusedSlots <- slot:
		returnedSlotCounter.Add(ctx, 1)
		reusableSlotsAvailableCounter.Add(ctx, 1)
		p.closeMu.RUnlock()

		return nil
	default:
		p.closeMu.RUnlock()

		if cerr := p.cleanup(ctx, slot); cerr != nil {
			return fmt.Errorf("failed to return slot '%d': %w", slot.Idx, cerr)
		}

		return nil
	}
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

	p.closeMu.Lock()
	p.closed = true
	p.closeMu.Unlock()

	var errs []error

	for slot := range p.newSlots {
		newSlotsAvailableCounter.Add(ctx, -1)

		err := p.cleanup(ctx, slot)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to cleanup slot '%d': %w", slot.Idx, err))
		}
	}

drain:
	for {
		select {
		case slot := <-p.reusedSlots:
			reusableSlotsAvailableCounter.Add(ctx, -1)

			err := p.cleanup(ctx, slot)
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to cleanup slot '%d': %w", slot.Idx, err))
			}
		default:
			break drain
		}
	}

	return errors.Join(errs...)
}
