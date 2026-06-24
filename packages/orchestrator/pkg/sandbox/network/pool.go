//go:build linux

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

	// 0 disables; valid range 0..63 (DSCP is 6 bits). CS1=8 is the canonical Scavenger class (RFC 3662).
	SandboxEgressDSCP uint8 `env:"SANDBOX_EGRESS_DSCP" envDefault:"0"`
}

func (c Config) Validate() error {
	if c.SandboxEgressDSCP > 63 {
		return fmt.Errorf("SANDBOX_EGRESS_DSCP=%d out of range (0..63)", c.SandboxEgressDSCP)
	}

	return nil
}

func ParseConfig() (Config, error) {
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

type Pool struct {
	config Config

	done     chan struct{}
	doneOnce sync.Once

	closeMu sync.RWMutex
	closed  bool

	newSlots    chan *Slot
	reusedSlots chan *Slot

	// returnsWG tracks in-flight asynchronous slot returns; Close waits for
	// it before draining the pool.
	returnsWG sync.WaitGroup

	slotStorage Storage
}

var ErrClosed = errors.New("cannot read from a closed pool")

func NewPool(newSlotsPoolSize, reusedSlotsPoolSize int, slotStorage Storage, config Config) *Pool {
	newSlots := make(chan *Slot, newSlotsPoolSize-1)
	reusedSlots := make(chan *Slot, reusedSlotsPoolSize)

	return &Pool{
		config:      config,
		done:        make(chan struct{}),
		newSlots:    newSlots,
		reusedSlots: reusedSlots,
		slotStorage: slotStorage,
	}
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

			select {
			case p.newSlots <- slot:
				newSlotsAvailableCounter.Add(ctx, 1)

				continue
			case <-p.done:
			case <-ctx.Done():
			}

			if err := p.cleanup(context.WithoutCancel(ctx), slot); err != nil {
				logger.L().Error(ctx, "[network slot pool]: failed to cleanup created slot while closing", zap.Error(err), zap.Int("slot_index", slot.Idx))
			}

			return
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
		// Return the slot to the pool if configuring internet fails. The slot
		// was never handed out, so nobody listens for its release notification.
		if rerr := p.ReturnAsync(context.WithoutCancel(ctx), slot, func(context.Context, string) {}, 0); rerr != nil {
			logger.L().Error(ctx, "failed to return slot to the pool", zap.Error(rerr), zap.Int("slot_index", slot.Idx))
		}

		return nil, fmt.Errorf("error setting slot internet access: %w", err)
	}

	return slot, nil
}

// returnSlot recycles a slot that was used by a sandbox. It waits returnDelay
// before making the slot reusable to let inflight requests on the previous
// sandbox drain.
func (p *Pool) returnSlot(ctx context.Context, slot *Slot, releasedFn ReleaseNotify, returnDelay time.Duration) error {
	notifyNetworkRelease := sync.OnceFunc(func() {
		releasedFn(ctx, slot.HostIPString())
	})
	// Make sure we notify for all code paths
	defer notifyNetworkRelease()

	// If the pool is closed or the context is cancelled during the delay we
	// still fall through and clean up the slot to avoid leaking it.
	select {
	case <-ctx.Done():
		return p.cleanupWith(ctx, slot, ctx.Err())
	case <-p.done:
		return p.cleanupWith(ctx, slot, ErrClosed)
	case <-time.After(returnDelay):
	}

	// Notify right before the release
	notifyNetworkRelease()

	return p.recycle(ctx, slot)
}

// tryTrackReturn registers an in-flight slot return so Close can wait for
// it. It reports false when the pool is already closed and the caller must
// process the slot inline.
func (p *Pool) tryTrackReturn() bool {
	p.closeMu.RLock()
	defer p.closeMu.RUnlock()

	if p.closed {
		return false
	}

	p.returnsWG.Add(1)

	return true
}

// ReturnAsync recycles a slot in the background, logging errors instead of
// returning them. Close waits for all in-flight returns before draining the
// pool. If the pool is already closed the slot is cleaned up synchronously.
func (p *Pool) ReturnAsync(ctx context.Context, slot *Slot, releasedFn ReleaseNotify, returnDelay time.Duration) error {
	if !p.tryTrackReturn() {
		return p.returnSlot(ctx, slot, releasedFn, returnDelay)
	}

	go func() {
		defer p.returnsWG.Done()

		err := p.returnSlot(ctx, slot, releasedFn, returnDelay)
		switch {
		case err == nil:
		case errors.Is(err, ErrClosed), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			// Expected when the pool closes or the context ends mid-return.
			logger.L().Warn(ctx, "network slot returned during pool shutdown", zap.Error(err), zap.Int("slot_index", slot.Idx))
		default:
			logger.L().Error(ctx, "failed to return network slot to pool", zap.Error(err), zap.Int("slot_index", slot.Idx))
		}
	}()

	return nil
}

// recycle resets the slot's internet configuration and puts it back into the
// reused pool, or cleans it up if the pool is full or closed.
func (p *Pool) recycle(ctx context.Context, slot *Slot) error {
	if err := slot.ResetInternet(ctx); err != nil {
		return p.cleanupWith(ctx, slot, fmt.Errorf("error resetting slot internet access: %w", err))
	}

	reused, cause := p.tryReuse(ctx, slot)
	if reused {
		return nil
	}

	// cause is nil when the pool is simply full.
	return p.cleanupWith(ctx, slot, cause)
}

// tryReuse attempts to push the slot into the reused pool. The RLock pairs
// with Close's Lock so a send can never race the drain; cleanup stays
// outside the lock so Close is never pinned by slow iptables/netlink
// teardown.
func (p *Pool) tryReuse(ctx context.Context, slot *Slot) (reused bool, cause error) {
	p.closeMu.RLock()
	defer p.closeMu.RUnlock()

	if p.closed {
		return false, ErrClosed
	}

	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-p.done:
		return false, ErrClosed
	case p.reusedSlots <- slot:
		returnedSlotCounter.Add(ctx, 1)
		reusableSlotsAvailableCounter.Add(ctx, 1)

		return true, nil
	default:
		return false, nil
	}
}

// cleanupWith tears the slot down and attaches any cleanup error to cause.
func (p *Pool) cleanupWith(ctx context.Context, slot *Slot, cause error) error {
	if cerr := p.cleanup(ctx, slot); cerr != nil {
		return errors.Join(cause, fmt.Errorf("cleanup slot '%d': %w", slot.Idx, cerr))
	}

	return cause
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

	// Wait for in-flight asynchronous returns: each either cleans its slot
	// up itself or has already pushed it into reusedSlots, drained below.
	p.returnsWG.Wait()

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
