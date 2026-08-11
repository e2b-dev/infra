//go:build linux

package v2

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var (
	meter = otel.Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network/v2")

	newSlotsAvailableCounter = utils.Must(meter.Int64UpDownCounter("orchestrator.network.v2.slots_pool.new",
		metric.WithDescription("Number of new v2 network slots ready to be used."),
		metric.WithUnit("{slot}"),
	))
	reusableSlotsAvailableCounter = utils.Must(meter.Int64UpDownCounter("orchestrator.network.v2.slots_pool.reused",
		metric.WithDescription("Number of reused v2 network slots ready to be used."),
		metric.WithUnit("{slot}"),
	))
	acquiredSlots = utils.Must(meter.Int64Counter("orchestrator.network.v2.slots_pool.acquired",
		metric.WithDescription("Number of v2 network slots acquired."),
		metric.WithUnit("{slot}"),
	))
	returnedSlotCounter = utils.Must(meter.Int64Counter("orchestrator.network.v2.slots_pool.returned",
		metric.WithDescription("Number of v2 network slots returned."),
		metric.WithUnit("{slot}"),
	))
	releasedSlotCounter = utils.Must(meter.Int64Counter("orchestrator.network.v2.slots_pool.released",
		metric.WithDescription("Number of v2 network slots released."),
		metric.WithUnit("{slot}"),
	))
)

// V2Pool implements network.PoolInterface using v2 networking (nftables only, zero iptables).
type V2Pool struct {
	config      network.Config
	storage     network.Storage
	hostFw      *HostFirewall
	observer    *VethObserver
	registry    *SlotV2Registry
	newSlots    chan *network.Slot
	reusedSlots chan *network.Slot
	done        chan struct{}
	doneOnce    sync.Once

	closeMu sync.RWMutex
	closed  bool

	// returnsWG tracks in-flight asynchronous slot returns; Close waits for
	// it before draining the pool.
	returnsWG sync.WaitGroup
}

// Compile-time check that V2Pool satisfies network.PoolInterface.
var _ network.PoolInterface = (*V2Pool)(nil)

// ValidateV2Prerequisites checks that required kernel parameters are set for v2 networking.
// Returns an error if any prerequisite is missing. Call before NewV2Pool.
func ValidateV2Prerequisites() error {
	checks := []struct {
		path     string
		expected string
		desc     string
	}{
		{"/proc/sys/net/ipv4/ip_forward", "1", "IPv4 forwarding"},
		{"/proc/sys/net/ipv4/conf/all/src_valid_mark", "1", "src_valid_mark (required for fwmark-based policy routing)"},
	}

	var errs []error
	for _, c := range checks {
		val, err := os.ReadFile(c.path)
		if err != nil {
			errs = append(errs, fmt.Errorf("cannot read %s: %w", c.path, err))

			continue
		}
		if strings.TrimSpace(string(val)) != c.expected {
			errs = append(errs, fmt.Errorf("%s: %s must be %s, got %q", c.desc, c.path, c.expected, strings.TrimSpace(string(val))))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("v2 networking prerequisites not met: %w", errors.Join(errs...))
	}

	return nil
}

func NewV2Pool(storage network.Storage, config network.Config, hostFw *HostFirewall, observer *VethObserver) *V2Pool {
	return &V2Pool{
		config:      config,
		storage:     storage,
		hostFw:      hostFw,
		observer:    observer,
		registry:    NewSlotV2Registry(),
		newSlots:    make(chan *network.Slot, network.NewSlotsPoolSize-1),
		reusedSlots: make(chan *network.Slot, network.ReusedSlotsPoolSize),
		done:        make(chan struct{}),
	}
}

func (p *V2Pool) createNetworkSlot(ctx context.Context) (*network.Slot, error) {
	slot, err := p.storage.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire network slot: %w", err)
	}

	slotV2 := NewSlotV2(slot)
	p.registry.Store(slotV2)

	if err := CreateNetworkV2(ctx, slot, slotV2, p.hostFw, p.observer); err != nil {
		p.registry.Delete(slot.Idx)
		releaseErr := p.storage.Release(slot)

		return nil, fmt.Errorf("failed to create v2 network: %w", errors.Join(err, releaseErr))
	}

	return slot, nil
}

func (p *V2Pool) Populate(ctx context.Context) {
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
				logger.L().Error(ctx, "[v2 network slot pool]: failed to create network", zap.Error(err))

				continue
			}

			newSlotsAvailableCounter.Add(ctx, 1)
			p.newSlots <- slot
		}
	}
}

// Get acquires a slot. The egress class is accepted for interface parity with
// the v1 pool; v2 does not stamp egress DSCP.
func (p *V2Pool) Get(ctx context.Context, netConfig *orchestrator.SandboxNetworkConfig, _ network.EgressClass) (*network.Slot, error) {
	var slot *network.Slot

	select {
	case <-p.done:
		return nil, network.ErrClosed
	case s := <-p.reusedSlots:
		reusableSlotsAvailableCounter.Add(ctx, -1)
		acquiredSlots.Add(ctx, 1, metric.WithAttributes(attribute.String("pool", "reused")))
		telemetry.ReportEvent(ctx, "reused v2 network slot")
		slot = s
	default:
		select {
		case <-p.done:
			return nil, network.ErrClosed
		case <-ctx.Done():
			return nil, ctx.Err()
		case s := <-p.newSlots:
			newSlotsAvailableCounter.Add(ctx, -1)
			acquiredSlots.Add(ctx, 1, metric.WithAttributes(attribute.String("pool", "new")))
			telemetry.ReportEvent(ctx, "new v2 network slot")
			slot = s
		}
	}

	if err := slot.ConfigureInternet(ctx, netConfig); err != nil {
		// Never handed out, so nobody listens for the release notification.
		if rerr := p.ReturnAsync(context.WithoutCancel(ctx), slot, func(context.Context, string) {}, 0); rerr != nil {
			logger.L().Error(ctx, "failed to return v2 slot to pool", zap.Error(rerr), zap.Int("slot_index", slot.Idx))
		}

		return nil, fmt.Errorf("error setting v2 slot internet access: %w", err)
	}

	return slot, nil
}

func (p *V2Pool) tryTrackReturn() bool {
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
func (p *V2Pool) ReturnAsync(ctx context.Context, slot *network.Slot, releasedFn network.ReleaseNotify, returnDelay time.Duration) error {
	if !p.tryTrackReturn() {
		return p.returnSlot(ctx, slot, releasedFn, returnDelay)
	}

	go func() {
		defer p.returnsWG.Done()

		err := p.returnSlot(ctx, slot, releasedFn, returnDelay)
		switch {
		case err == nil:
		case errors.Is(err, network.ErrClosed), errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			logger.L().Warn(ctx, "v2 network slot returned during pool shutdown", zap.Error(err), zap.Int("slot_index", slot.Idx))
		default:
			logger.L().Error(ctx, "failed to return v2 network slot to pool", zap.Error(err), zap.Int("slot_index", slot.Idx))
		}
	}()

	return nil
}

// returnSlot recycles a slot that was used by a sandbox. It waits returnDelay
// before making the slot reusable to let inflight requests on the previous
// sandbox drain.
func (p *V2Pool) returnSlot(ctx context.Context, slot *network.Slot, releasedFn network.ReleaseNotify, returnDelay time.Duration) error {
	notifyNetworkRelease := sync.OnceFunc(func() {
		releasedFn(ctx, slot.HostIPString())
	})
	defer notifyNetworkRelease()

	// If the pool is closed or the context is cancelled during the delay we
	// still fall through and clean up the slot to avoid leaking it.
	select {
	case <-ctx.Done():
		return p.cleanupWith(ctx, slot, ctx.Err())
	case <-p.done:
		return p.cleanupWith(ctx, slot, network.ErrClosed)
	case <-time.After(returnDelay):
	}

	notifyNetworkRelease()

	return p.recycle(ctx, slot)
}

func (p *V2Pool) recycle(ctx context.Context, slot *network.Slot) error {
	if err := slot.ResetInternet(ctx); err != nil {
		return p.cleanupWith(ctx, slot, fmt.Errorf("error resetting v2 slot internet access: %w", err))
	}

	reused, cause := p.tryReuse(ctx, slot)
	if reused {
		return nil
	}

	// cause is nil when the pool is simply full.
	return p.cleanupWith(ctx, slot, cause)
}

// tryReuse attempts to push the slot into the reused pool. The RLock pairs
// with Close's Lock so a send can never race the drain.
func (p *V2Pool) tryReuse(ctx context.Context, slot *network.Slot) (reused bool, cause error) {
	p.closeMu.RLock()
	defer p.closeMu.RUnlock()

	if p.closed {
		return false, network.ErrClosed
	}

	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-p.done:
		return false, network.ErrClosed
	case p.reusedSlots <- slot:
		returnedSlotCounter.Add(ctx, 1)
		reusableSlotsAvailableCounter.Add(ctx, 1)

		return true, nil
	default:
		return false, nil
	}
}

// cleanupWith tears the slot down and attaches any cleanup error to cause.
func (p *V2Pool) cleanupWith(ctx context.Context, slot *network.Slot, cause error) error {
	if cerr := p.cleanup(ctx, slot); cerr != nil {
		return errors.Join(cause, fmt.Errorf("cleanup v2 slot '%d': %w", slot.Idx, cerr))
	}

	return cause
}

func (p *V2Pool) cleanup(ctx context.Context, slot *network.Slot) error {
	var errs []error

	slotV2, ok := p.registry.Load(slot.Idx)
	if ok {
		if err := RemoveNetworkV2(ctx, slot, slotV2, p.hostFw, p.observer); err != nil {
			errs = append(errs, fmt.Errorf("cannot remove v2 network for slot '%d': %w", slot.Idx, err))
		}
		p.registry.Delete(slot.Idx)
	} else {
		// Fallback: try basic cleanup even without v2 metadata
		if err := slot.RemoveNetwork(); err != nil {
			errs = append(errs, fmt.Errorf("cannot remove network for slot '%d': %w", slot.Idx, err))
		}
	}

	if err := p.storage.Release(slot); err != nil {
		errs = append(errs, fmt.Errorf("failed to release slot '%d': %w", slot.Idx, err))
	}

	releasedSlotCounter.Add(ctx, 1)

	return errors.Join(errs...)
}

func (p *V2Pool) Close(ctx context.Context) error {
	logger.L().Info(ctx, "Closing v2 network pool")

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

		if err := p.cleanup(ctx, slot); err != nil {
			errs = append(errs, fmt.Errorf("failed to cleanup v2 slot '%d': %w", slot.Idx, err))
		}
	}

drain:
	for {
		select {
		case slot := <-p.reusedSlots:
			reusableSlotsAvailableCounter.Add(ctx, -1)

			if err := p.cleanup(ctx, slot); err != nil {
				errs = append(errs, fmt.Errorf("failed to cleanup v2 slot '%d': %w", slot.Idx, err))
			}
		default:
			break drain
		}
	}

	if err := p.hostFw.Close(); err != nil {
		errs = append(errs, fmt.Errorf("failed to close host firewall: %w", err))
	}

	if err := p.observer.Close(); err != nil {
		errs = append(errs, fmt.Errorf("failed to close observer: %w", err))
	}

	return errors.Join(errs...)
}
