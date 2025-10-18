package network

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const maxAttempts = 25

var (
	ErrNoSlotFound = fmt.Errorf("failed to acquire IP slot")

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
	config   Config
	maxSlots int

	// these are only to improve performance,
	// we rely on the files to prevent us from doing bad things
	acquiredNs   map[int]struct{}
	acquiredNsMu sync.Mutex

	pid int
}

func NewStorageLocal(config Config) (*StorageLocal, error) {
	if err := os.MkdirAll(config.LocalNamespaceStorageDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create storage dir: %w", err)
	}

	return &StorageLocal{
		config:   config,
		maxSlots: config.GetVirtualSlotsSize(),

		acquiredNs: make(map[int]struct{}),
		pid:        os.Getpid(),
	}, nil
}

func (s *StorageLocal) Acquire(ctx context.Context) (*Slot, error) {
	ctx, span := tracer.Start(ctx, "network-namespace-acquire")
	defer span.End()

	for attempt := range maxAttempts {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if attempt != 0 {
			acquisitionRetries.Add(ctx, 1)
		}

		slotIdx := rand.Intn(s.maxSlots) + 1

		if slot, ok := s.tryAcquire(ctx, slotIdx); ok {
			return slot, nil
		}
	}

	return nil, ErrNoSlotFound
}

func (s *StorageLocal) tryAcquire(ctx context.Context, slotIdx int) (*Slot, bool) {
	s.acquiredNsMu.Lock()
	defer s.acquiredNsMu.Unlock()

	// skip the slot if it's already acquired
	if _, found := s.acquiredNs[slotIdx]; found {
		return nil, false
	}

	if err := s.lockSlot(slotIdx); err != nil {
		zap.L().Warn("failed to reserve network slot",
			zap.Error(err),
			zap.Int("slotIdx", slotIdx),
		)
		return nil, false
	}

	slotName := s.getSlotName(slotIdx)
	slot, err := NewSlot(slotName, slotIdx, s.config)
	if err != nil {
		s.unlockSlot(slotIdx)
		zap.L().Warn("failed to create network slot",
			zap.Error(err),
			zap.Int("slotIdx", slotIdx),
			zap.String("slotName", slotName),
		)
		return nil, false
	}

	s.acquiredNs[slotIdx] = struct{}{}

	acquisitions.Add(ctx, 1)

	return slot, true
}

func (s *StorageLocal) createPidFilename(slotIdx int) string {
	return filepath.Join(s.config.LocalNamespaceStorageDir, fmt.Sprintf("%d.pid", slotIdx))
}

func (s *StorageLocal) lockSlot(slotIdx int) error {
	fullPath := s.createPidFilename(slotIdx)
	fileContent := []byte(fmt.Sprintf("%d", s.pid))

	file, err := os.OpenFile(fullPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open file %q: %w", fullPath, err)
	}
	defer file.Close()

	if _, err = file.Write(fileContent); err != nil {
		return fmt.Errorf("failed to write to file %q: %w", fullPath, err)
	}

	return nil
}

func (s *StorageLocal) unlockSlot(slotIdx int) {
	fullPath := s.createPidFilename(slotIdx)

	if err := os.Remove(fullPath); err != nil {
		zap.L().Warn("failed to remove file",
			zap.Error(err),
			zap.String("fullPath", fullPath),
		)
	}
}

func (s *StorageLocal) Release(ctx context.Context, ips *Slot) error {
	releases.Add(ctx, 1)

	s.acquiredNsMu.Lock()
	defer s.acquiredNsMu.Unlock()

	delete(s.acquiredNs, ips.Idx)

	s.unlockSlot(ips.Idx)

	return nil
}

func (s *StorageLocal) getSlotName(slotIdx int) string {
	return fmt.Sprintf("ns-%d-%d", s.pid, slotIdx)
}
