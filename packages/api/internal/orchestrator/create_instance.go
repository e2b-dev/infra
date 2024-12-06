package orchestrator

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var httpClient = &http.Client{
	Timeout: 1 * time.Second,
}

func (o *Orchestrator) CreateSandbox(
	t trace.Tracer,
	ctx context.Context,
	sandboxID,
	templateID,
	alias,
	teamID string,
	build *models.EnvBuild,
	maxInstanceLengthHours int64,
	metadata,
	envVars map[string]string,
	kernelVersion,
	firecrackerVersion,
	envdVersion string,
	startTime time.Time,
	endTime time.Time,
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
			BuildID:            build.ID.String(),
			SandboxID:          sandboxID,
			KernelVersion:      kernelVersion,
			FirecrackerVersion: firecrackerVersion,
			EnvdVersion:        envdVersion,
			Metadata:           metadata,
			EnvVars:            envVars,
			MaxInstanceLength:  maxInstanceLengthHours,
			HugePages:          features.HasHugePages(),
			MemoryMB:           int32(build.RAMMB),
			VCpuCount:          int32(build.Vcpu),
		},
		StartTime: timestamppb.New(startTime),
		EndTime:   timestamppb.New(endTime),
	})

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return nil, fmt.Errorf("failed to create sandbox '%s': %w", templateID, err)
	}

	go func() {
		envdUrl, err := url.Parse(fmt.Sprintf("http://49983-%s-%s.localhost:3003/init", sandboxID, res.ClientID))
		if err != nil {
			log.Printf("failed to parse envd URL: %v", err)
		}
		r, err := httpClient.Do(&http.Request{
			Method: http.MethodPost,
			URL:    envdUrl,
		})
		if err != nil {
			log.Printf("failed to call envd: %v", err)
		}
		defer r.Body.Close()

		if r.StatusCode != http.StatusOK {
			log.Printf("envd returned non-200 status code: %d", r)
		}

		_, err = io.Copy(io.Discard, r.Body)
		if err != nil {
			log.Printf("failed to read envd response: %v", err)
		}
	}()

	telemetry.ReportEvent(childCtx, "Created sandbox")

	return &api.Sandbox{
		ClientID:    res.ClientID,
		SandboxID:   sandboxID,
		TemplateID:  templateID,
		Alias:       &alias,
		EnvdVersion: *build.EnvdVersion,
	}, nil
}
