package orchestrator

import (
	"context"
	_ "embed"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (o *Orchestrator) CreateSandbox(
	t trace.Tracer,
	ctx context.Context,
	sandboxID,
	templateID,
	alias,
	teamID,
	buildID string,
	maxInstanceLengthHours int64,
	metadata map[string]string,
	kernelVersion,
	firecrackerVersion string,
	vCPU int64,
	ramMB int64,
) (*api.Sandbox, error) {
	childCtx, childSpan := t.Start(ctx, "create-sandbox",
		trace.WithAttributes(
			attribute.String("env.id", templateID),
		),
	)
	defer childSpan.End()

	features, err := sandbox.NewVersionInfo(firecrackerVersion)
	if err != nil {
		errMsg := fmt.Errorf("failed to get features for firecracker version '%s': %w", firecrackerVersion, err)

		return nil, errMsg
	}

	telemetry.ReportEvent(childCtx, "Got FC version info")

	res, err := o.grpc.Sandbox.Create(ctx, &orchestrator.SandboxCreateRequest{
		Sandbox: &orchestrator.SandboxConfig{
			TemplateID:         templateID,
			Alias:              &alias,
			TeamID:             teamID,
			BuildID:            buildID,
			SandboxID:          sandboxID,
			KernelVersion:      kernelVersion,
			FirecrackerVersion: firecrackerVersion,
			Metadata:           metadata,
			MaxInstanceLength:  maxInstanceLengthHours,
			HugePages:          features.HasHugePages(),
			VCpu:               vCPU,
			RamMB:              ramMB,
		},
	})

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return nil, fmt.Errorf("failed to create sandbox '%s': %w", templateID, err)
	}

	telemetry.ReportEvent(childCtx, "Created sandbox")

	return &api.Sandbox{
		ClientID:   res.ClientID,
		SandboxID:  sandboxID,
		TemplateID: templateID,
		Alias:      &alias,
	}, nil
}
