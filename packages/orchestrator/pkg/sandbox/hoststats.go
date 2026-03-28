package sandbox

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/clickhouse/pkg/hoststats"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

// initializeHostStatsCollector initializes the host stats collector for a sandbox.
// This is a best-effort operation - errors are logged but do not fail the sandbox initialization.
func initializeHostStatsCollector(
	ctx context.Context,
	sbx *Sandbox,
	runtime RuntimeMetadata,
	config *Config,
	hostStatsDelivery hoststats.Delivery,
	samplingInterval time.Duration,
) {
	teamID, err := uuid.Parse(runtime.TeamID)
	if err != nil {
		logger.L().Warn(ctx, "invalid team ID for host stats, using zero UUID",
			logger.WithTeamID(runtime.TeamID), zap.Error(err))
	}

	collector := NewHostStatsCollector(
		HostStatsMetadata{
			SandboxID:   runtime.SandboxID,
			ExecutionID: runtime.ExecutionID,
			TemplateID:  runtime.TemplateID,
			BuildID:     runtime.BuildID,
			TeamID:      teamID,
			VCPUCount:   config.Vcpu,
			MemoryMB:    config.RamMB,
			SandboxType: runtime.SandboxType,
		},
		hostStatsDelivery,
		samplingInterval,
		sbx.cgroupHandle.GetStats,
	)

	sbx.hostStatsCollector = collector

	go collector.Start(ctx)
}
