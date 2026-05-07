package orchestrator

import (
	"context"
	"time"

	"github.com/posthog/posthog-go"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/middleware/otel/tracing"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	// reportTimeout is the timeout for the analytics report
	// This timeout is also set in the CloudRun for Analytics Collector, there it is 3 minutes.
	reportTimeout = 4 * time.Minute
)

func (o *Orchestrator) analyticsRemove(ctx context.Context, sandbox sandbox.Sandbox, stateAction sandbox.StateAction) {
	ctx, cancel := context.WithTimeout(ctx, reportTimeout)
	defer cancel()

	duration := time.Since(sandbox.StartTime).Seconds()
	stopTime := time.Now()

	o.posthogClient.CreateAnalyticsTeamEvent(
		ctx,
		sandbox.TeamID.String(),
		"closed_instance", posthog.NewProperties().
			Set("instance_id", sandbox.SandboxID).
			Set("environment", sandbox.TemplateID).
			Set("state_action", stateAction.Name).
			Set("duration", duration),
	)

	_, err := o.analytics.InstanceStopped(ctx, &analyticscollector.InstanceStoppedEvent{
		TeamId:        sandbox.TeamID.String(),
		EnvironmentId: sandbox.TemplateID,
		InstanceId:    sandbox.SandboxID,
		ExecutionId:   sandbox.ExecutionID,
		Timestamp:     timestamppb.New(stopTime),
		Duration:      float32(duration),
		CpuCount:      sandbox.VCpu,
		RamMb:         sandbox.RamMB,
		DiskSizeMb:    sandbox.TotalDiskSizeMB,
	})
	if err != nil {
		logger.L().Error(ctx, "error sending Analytics event", zap.Error(err))
	}
}

func (o *Orchestrator) analyticsInsert(ctx context.Context, sandbox sandbox.Sandbox) {
	ctx, cancel := context.WithTimeout(ctx, reportTimeout)
	defer cancel()

	_, err := o.analytics.InstanceStarted(ctx, &analyticscollector.InstanceStartedEvent{
		InstanceId:    sandbox.SandboxID,
		ExecutionId:   sandbox.ExecutionID,
		EnvironmentId: sandbox.TemplateID,
		BuildId:       sandbox.BuildID.String(),
		TeamId:        sandbox.TeamID.String(),
		CpuCount:      sandbox.VCpu,
		RamMb:         sandbox.RamMB,
		DiskSizeMb:    sandbox.TotalDiskSizeMB,
		Timestamp:     timestamppb.Now(),
	})
	if err != nil {
		logger.L().Error(ctx, "Error sending Analytics event", zap.Error(err))
	}
}

func (o *Orchestrator) emitCreatedInstancePosthog(ctx context.Context, sbx sandbox.Sandbox, meta sandbox.CreationMetadata, startDuration time.Duration) {
	o.posthogClient.IdentifyAnalyticsTeam(ctx, sbx.TeamID.String(), meta.TeamName)
	properties := o.posthogClient.GetPackageToPosthogProperties(&meta.RequestHeader)

	props := properties.
		Set("environment", sbx.TemplateID).
		Set("instance_id", sbx.SandboxID).
		Set("alias", sbx.Alias).
		Set("resume", meta.IsResume).
		Set("build_id", sbx.BuildID).
		Set("envd_version", sbx.EnvdVersion).
		Set("firecracker_version", sbx.FirecrackerVersion).
		Set("kernel_version", sbx.KernelVersion).
		Set("node_id", sbx.NodeID).
		Set("vcpu", sbx.VCpu).
		Set("ram_mb", sbx.RamMB).
		Set("total_disk_size_mb", sbx.TotalDiskSizeMB).
		Set("auto_pause", sbx.Lifecycle.AutoPause)
	if startDuration > 0 {
		props = props.Set("start_time_ms", startDuration.Milliseconds())
	}

	if len(meta.MCPServerNames) > 0 {
		props = props.Set("mcp_servers", meta.MCPServerNames)
	}

	if len(sbx.VolumeMounts) > 0 {
		volumeNames := make([]string, 0, len(sbx.VolumeMounts))
		volumeIDs := make([]string, 0, len(sbx.VolumeMounts))
		for _, vol := range sbx.VolumeMounts {
			volumeNames = append(volumeNames, vol.Name)
			volumeIDs = append(volumeIDs, vol.ID)
		}
		props = props.
			Set("volume_names", volumeNames).
			Set("volume_ids", volumeIDs).
			Set("volume_count", len(sbx.VolumeMounts))
	}

	o.posthogClient.CreateAnalyticsTeamEvent(ctx, sbx.TeamID.String(), "created_instance", props)
}

func logSandboxCreated(ctx context.Context, sbx sandbox.Sandbox) {
	logMetadata := &sbxlogger.SandboxMetadata{
		SandboxID:  sbx.SandboxID,
		TemplateID: sbx.TemplateID,
		TeamID:     sbx.TeamID.String(),
	}

	endTimeStr := sbx.EndTime.Format("2006-01-02 15:04:05 -07:00")
	sbxlogger.E(logMetadata).Info(ctx, "Sandbox created", zap.String("end_time", endTimeStr))

	autoResumePolicy := "unset"
	if sbx.Lifecycle.AutoResume != nil {
		autoResumePolicy = string(sbx.Lifecycle.AutoResume.Policy)
	}

	sbxlogger.I(logMetadata).Info(
		ctx,
		"Sandbox created details",
		zap.String("end_time", endTimeStr),
		zap.String("auto_resume_policy", autoResumePolicy),
		zap.Bool("auto_pause", sbx.Lifecycle.AutoPause),
		zap.String("template_id", sbx.BaseTemplateID),
	)
}

func (o *Orchestrator) handleNewlyCreatedSandbox(ctx context.Context, sbx sandbox.Sandbox, meta sandbox.CreationMetadata) {
	ctx, span := tracer.Start(ctx, "newly-created-sandbox-callback")
	defer span.End()

	// Calculate the time it took for the sandbox to start from request receipt
	var startDuration time.Duration
	if requestStartTime, ok := tracing.GetRequestStartTime(ctx); ok {
		startDuration = time.Since(requestStartTime)
	}

	o.analyticsInsert(ctx, sbx)
	o.emitCreatedInstancePosthog(ctx, sbx, meta, startDuration)
	o.teamMetricsObserver.Add(ctx, sbx.TeamID)
	o.createdCounter.Add(ctx, 1, metric.WithAttributes(telemetry.WithTeamID(sbx.TeamID.String())))
	logSandboxCreated(ctx, sbx)
}
