package orchestrator

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
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
		envdCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		envdErr := initEnvd(envdCtx, envVars, sandboxID, res.ClientID)
		if envdErr != nil {
			log.Printf(fmt.Sprintf("failed to init envd: %v", envdErr))
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

type PostInitJSONBody struct {
	EnvVars *map[string]string `json:"envVars"`
}

const maxRetries = 30

func initEnvd(ctx context.Context, envVars map[string]string, sandboxID, clientID string) error {
	address := fmt.Sprintf("https://%d-%s-%s.goulash.dev/init", consts.DefaultEnvdServerPort, sandboxID, clientID)

	counter := 0

	jsonBody := &PostInitJSONBody{
		EnvVars: &envVars,
	}

	envVarsJSON, err := json.Marshal(jsonBody)
	if err != nil {
		return err
	}

	var response *http.Response
	for counter <= maxRetries {
		reqCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		request, err := http.NewRequestWithContext(reqCtx, "POST", address, bytes.NewReader(envVarsJSON))
		if err != nil {
			cancel()
			return fmt.Errorf("failed to create request: %w", err)
		}
		cancel()

		response, err = httpClient.Do(request)
		if err != nil {
			counter++
			time.Sleep(10 * time.Millisecond)

			continue
		}

	}

	if response == nil {
		return fmt.Errorf("failed to init envd")
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected status code: %d", response.StatusCode)
	}

	_, err = io.Copy(io.Discard, response.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	return nil

}
