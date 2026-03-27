package handlers

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/middleware/otel/tracing"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	typesteam "github.com/e2b-dev/infra/packages/auth/pkg/types"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/api/internal/handlers")

func (a *APIStore) startSandbox(
	ctx context.Context,
	sandboxID string,
	timeout time.Duration,
	team *typesteam.Team,
	getSandboxData orchestrator.SandboxDataFetcher,
	requestHeader *http.Header,
	isResume bool,
	mcp api.Mcp,
) (*api.Sandbox, *api.APIError) {
	sbx, apiErr := a.startSandboxInternal(
		ctx,
		sandboxID,
		timeout,
		team,
		getSandboxData,
		requestHeader,
		isResume,
		mcp,
	)
	if apiErr != nil {
		return nil, apiErr
	}

	return sbx.ToAPISandbox(), nil
}

// startSandboxInternal starts the sandbox and returns the internal sandbox model (includes routing info).
func (a *APIStore) startSandboxInternal(
	ctx context.Context,
	sandboxID string,
	timeout time.Duration,
	team *typesteam.Team,
	getSandboxData orchestrator.SandboxDataFetcher,
	requestHeader *http.Header,
	isResume bool,
	mcp api.Mcp,
) (sandbox.Sandbox, *api.APIError) {
	startTime := time.Now()
	endTime := startTime.Add(timeout)

	// Unique ID for the execution (from start/resume to stop/pause)
	executionID := uuid.New().String()
	sbx, instanceErr := a.orchestrator.CreateSandbox(
		ctx,
		sandboxID,
		executionID,
		team,
		getSandboxData,
		startTime,
		endTime,
		timeout,
		isResume,
	)
	if instanceErr != nil {
		telemetry.ReportError(ctx, "error when creating instance", instanceErr.Err)

		return sandbox.Sandbox{}, instanceErr
	}

	telemetry.ReportEvent(ctx, "Created sandbox")

	_, analyticsSpan := tracer.Start(ctx, "analytics")
	a.posthog.IdentifyAnalyticsTeam(ctx, team.ID.String(), team.Name)
	properties := a.posthog.GetPackageToPosthogProperties(requestHeader)
	props := properties.
		Set("environment", sbx.TemplateID).
		Set("instance_id", sbx.SandboxID).
		Set("alias", sbx.Alias).
		Set("resume", isResume).
		Set("build_id", sbx.BuildID).
		Set("envd_version", sbx.EnvdVersion).
		Set("node_id", sbx.NodeID).
		Set("vcpu", sbx.VCpu).
		Set("ram_mb", sbx.RamMB).
		Set("total_disk_size_mb", sbx.TotalDiskSizeMB).
		Set("auto_pause", sbx.AutoPause)

	// Calculate the time it took for the sandbox to start from request receipt
	if requestStartTime, ok := tracing.GetRequestStartTime(ctx); ok {
		props = props.Set("start_time_ms", time.Since(requestStartTime).Milliseconds())
	}

	if mcp != nil {
		props = props.Set("mcp_servers", slices.Collect(maps.Keys(mcp)))
	}

	a.posthog.CreateAnalyticsTeamEvent(ctx, team.ID.String(), "created_instance", props)
	analyticsSpan.End()

	telemetry.ReportEvent(ctx, "Created analytics event")

	go func() {
		a.templateSpawnCounter.IncreaseTemplateSpawnCount(sbx.BaseTemplateID, time.Now())
	}()

	telemetry.SetAttributes(ctx,
		attribute.String("instance.id", sbx.SandboxID),
	)

	logMetadata := &sbxlogger.SandboxMetadata{
		SandboxID:  sbx.SandboxID,
		TemplateID: sbx.TemplateID,
		TeamID:     team.ID.String(),
	}
	sbxlogger.E(logMetadata).Info(ctx, "Sandbox created", zap.String("end_time", endTime.Format("2006-01-02 15:04:05 -07:00")))

	autoResumePolicy := "unset"
	if sbx.AutoResume != nil {
		autoResumePolicy = string(sbx.AutoResume.Policy)
	}

	sbxlogger.I(logMetadata).Info(
		ctx,
		"Sandbox created details",
		zap.String("end_time", endTime.Format("2006-01-02 15:04:05 -07:00")),
		zap.String("auto_resume_policy", autoResumePolicy),
		zap.Bool("auto_pause", sbx.AutoPause),
		zap.String("parent_template_id", sbx.BaseTemplateID),
	)

	return sbx, nil
}
