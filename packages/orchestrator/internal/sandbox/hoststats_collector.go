package sandbox

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shirou/gopsutil/v4/process"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/clickhouse/pkg/hoststats"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type HostStatsCollector struct {
	metadata         HostStatsMetadata
	delivery         hoststats.Delivery
	proc             *process.Process
	samplingInterval time.Duration

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
}

func NewHostStatsCollector(
	metadata HostStatsMetadata,
	firecrackerPID int32,
	delivery hoststats.Delivery,
	samplingInterval time.Duration,
) (*HostStatsCollector, error) {
	// Validate and enforce minimum interval
	if samplingInterval < 100*time.Millisecond {
		samplingInterval = 100 * time.Millisecond
	}

	proc, err := process.NewProcess(firecrackerPID)
	if err != nil {
		return nil, fmt.Errorf("failed to create process handle: %w", err)
	}

	return &HostStatsCollector{
		metadata:         metadata,
		delivery:         delivery,
		proc:             proc,
		samplingInterval: samplingInterval,
		stopCh:           make(chan struct{}),
		stoppedCh:        make(chan struct{}),
	}, nil
}

// CollectSample collects a single host statistics sample for the Firecracker process
func (h *HostStatsCollector) CollectSample(ctx context.Context) error {
	// Get CPU times (user and system)
	times, err := h.proc.TimesWithContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get CPU times: %w", err)
	}

	// Get memory info (RSS and VMS)
	memInfo, err := h.proc.MemoryInfoWithContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get memory info: %w", err)
	}

	stat := hoststats.SandboxHostStat{
		Timestamp:                time.Now(),
		SandboxID:                h.metadata.SandboxID,
		SandboxExecutionID:       h.metadata.ExecutionID,
		SandboxTemplateID:        h.metadata.TemplateID,
		SandboxBuildID:           h.metadata.BuildID,
		SandboxTeamID:            h.metadata.TeamID,
		SandboxVCPUCount:         h.metadata.VCPUCount,
		SandboxMemoryMB:          h.metadata.MemoryMB,
		FirecrackerCPUUserTime:   times.User,   // seconds
		FirecrackerCPUSystemTime: times.System, // seconds
		FirecrackerMemoryRSS:     memInfo.RSS,  // bytes
		FirecrackerMemoryVMS:     memInfo.VMS,  // bytes
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
			zap.String("sandbox_id", h.metadata.SandboxID),
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
					zap.String("sandbox_id", h.metadata.SandboxID),
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
				zap.String("sandbox_id", h.metadata.SandboxID),
				zap.Error(err))
		}
	})
}
