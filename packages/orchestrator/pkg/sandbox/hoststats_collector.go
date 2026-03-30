package sandbox

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/clickhouse/pkg/hoststats"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/cgroup"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// CgroupStatsFunc is a function that returns current cgroup resource usage statistics.
type CgroupStatsFunc func(ctx context.Context) (*cgroup.Stats, error)

type HostStatsCollector struct {
	metadata         HostStatsMetadata
	delivery         hoststats.Delivery
	samplingInterval time.Duration
	cgroupStats      CgroupStatsFunc

	stopCh    chan struct{}
	stoppedCh chan struct{}
	stopOnce  sync.Once
}

type HostStatsMetadata struct {
	SandboxID   string
	ExecutionID string
	TemplateID  string
	BuildID     string
	TeamID      uuid.UUID
	VCPUCount   int64
	MemoryMB    int64
	SandboxType SandboxType
}

func NewHostStatsCollector(
	metadata HostStatsMetadata,
	delivery hoststats.Delivery,
	samplingInterval time.Duration,
	cgroupStats CgroupStatsFunc,
) *HostStatsCollector {
	// Validate and enforce minimum interval
	if samplingInterval < 100*time.Millisecond {
		samplingInterval = 100 * time.Millisecond
	}

	return &HostStatsCollector{
		metadata:         metadata,
		delivery:         delivery,
		samplingInterval: samplingInterval,
		cgroupStats:      cgroupStats,
		stopCh:           make(chan struct{}),
		stoppedCh:        make(chan struct{}),
	}
}

// CollectSample collects a single cgroup statistics sample for the sandbox.
func (h *HostStatsCollector) CollectSample(ctx context.Context) error {
	cgroupStats, err := h.cgroupStats(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cgroup stats: %w", err)
	}

	if cgroupStats == nil {
		return nil
	}

	stat := hoststats.SandboxHostStat{
		Timestamp:           time.Now(),
		SandboxID:           h.metadata.SandboxID,
		SandboxExecutionID:  h.metadata.ExecutionID,
		SandboxTemplateID:   h.metadata.TemplateID,
		SandboxBuildID:      h.metadata.BuildID,
		SandboxTeamID:       h.metadata.TeamID,
		SandboxVCPUCount:    h.metadata.VCPUCount,
		SandboxMemoryMB:     h.metadata.MemoryMB,
		CgroupCPUUsageUsec:  cgroupStats.CPUUsageUsec,
		CgroupCPUUserUsec:   cgroupStats.CPUUserUsec,
		CgroupCPUSystemUsec: cgroupStats.CPUSystemUsec,
		CgroupMemoryUsage:   cgroupStats.MemoryUsageBytes,
		CgroupMemoryPeak:    cgroupStats.MemoryPeakBytes,
		SandboxType:         h.metadata.SandboxType.String(),
	}

	if err := h.delivery.Push(stat); err != nil {
		return fmt.Errorf("failed to push stat to delivery: %w", err)
	}

	return nil
}

// Start begins periodic collection of host statistics
func (h *HostStatsCollector) Start(ctx context.Context) {
	defer close(h.stoppedCh)

	// Collect initial sample before starting periodic collection
	if err := h.CollectSample(ctx); err != nil {
		// Log error but continue with periodic sampling - don't kill the sandbox
		logger.L().Error(ctx, "failed to collect initial host stats sample",
			logger.WithSandboxID(h.metadata.SandboxID),
			zap.Error(err))
	}

	ticker := time.NewTicker(h.samplingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := h.CollectSample(ctx); err != nil {
				// Log error but continue sampling - don't kill the sandbox
				logger.L().Error(ctx, "failed to collect host stats sample",
					logger.WithSandboxID(h.metadata.SandboxID),
					zap.Error(err))
			}
		case <-h.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

// Stop halts periodic collection and takes a final sample
func (h *HostStatsCollector) Stop(ctx context.Context) {
	h.stopOnce.Do(func() {
		close(h.stopCh)
		<-h.stoppedCh

		// Take final sample before process terminates
		if err := h.CollectSample(ctx); err != nil {
			// Log but don't fail the shutdown
			logger.L().Error(ctx, "failed to collect final host stats sample",
				logger.WithSandboxID(h.metadata.SandboxID),
				zap.Error(err))
		}
	})
}
