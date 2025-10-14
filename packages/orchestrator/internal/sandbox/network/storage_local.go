package network

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var (
	meter = otel.GetMeterProvider().Meter(
		"orchestrator.internal.sandbox.network.storage.local",
	)
	acquisitions = utils.Must(meter.Int64Counter("orchestrator.sandbox.network.ns_acquisitions",
		metric.WithDescription("number of network namespace acquisitions"),
	))
	acquisitionRetries = utils.Must(meter.Int64Counter("orchestrator.sandbox.network.ns_acquisition_retries",
		metric.WithDescription("number of network namespace acquisition retries"),
	))
	releases = utils.Must(meter.Int64Counter("orchestrator.sandbox.network.ns_releases",
		metric.WithDescription("number of network namespace releases"),
	))
)

type StorageLocal struct {
	config       Config
	slotsSize    int
	acquiredNs   map[string]struct{}
	acquiredNsMu sync.Mutex

	pid int
}

const (
	netNamespacesDir = "/var/run/netns"
	maxAttempts      = 25
)

func NewStorageLocal(slotsSize int, config Config) (*StorageLocal, error) {
	return &StorageLocal{
		config:       config,
		slotsSize:    slotsSize,
		acquiredNs:   make(map[string]struct{}, slotsSize),
		acquiredNsMu: sync.Mutex{},
		pid:          os.Getpid(),
	}, nil
}

var (
	ErrTimeout               = fmt.Errorf("failed to acquire IP slot: timeout")
	ErrNetworkSlotsExhausted = fmt.Errorf("failed to acquire IP slot: no empty slots found")
)

func (s *StorageLocal) Acquire(ctx context.Context) (*Slot, error) {
	ctx, span := tracer.Start(ctx, "network-namespace-acquire")
	defer span.End()

	s.acquiredNsMu.Lock()
	defer s.acquiredNsMu.Unlock()

	for attempt := range maxAttempts {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if len(s.acquiredNs) >= s.slotsSize {
			return nil, ErrNetworkSlotsExhausted
		}

		if attempt != 0 {
			acquisitionRetries.Add(ctx, 1)
		}

		slotIdx := rand.Intn(s.slotsSize) + 1
		slotName := s.getSlotName(slotIdx)

		// skip the slot if it's already acquired
		if _, found := s.acquiredNs[slotName]; found {
			continue
		}

		slot, err := NewSlot(slotName, slotIdx, s.config)
		if err != nil {
			zap.L().Warn("failed to create network slot",
				zap.Error(err),
				zap.Int("slotIdx", slotIdx),
				zap.String("slotName", slotName),
			)
			continue
		}

		s.acquiredNs[slotName] = struct{}{}
		acquisitions.Add(ctx, 1)

		return slot, nil
	}

	return nil, ErrTimeout
}

func (s *StorageLocal) Release(ctx context.Context, ips *Slot) error {
	releases.Add(ctx, 1)

	s.acquiredNsMu.Lock()
	defer s.acquiredNsMu.Unlock()

	slotName := s.getSlotName(ips.Idx)
	delete(s.acquiredNs, slotName)

	return nil
}

func (s *StorageLocal) getSlotName(slotIdx int) string {
	return fmt.Sprintf("ns-%d-%d", s.pid, slotIdx)
}
