package commands

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

type Env struct{}

func (e *Env) Execute(
	ctx context.Context,
	tracer trace.Tracer,
	postProcessor *writer.PostProcessor,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	prefix string,
	step *templatemanager.TemplateStep,
	cmdMetadata sandboxtools.CommandMetadata,
) (sandboxtools.CommandMetadata, error) {
	cmdType := strings.ToUpper(step.Type)
	args := step.Args
	// args: [key value]
	if len(args) < 2 {
		return sandboxtools.CommandMetadata{}, fmt.Errorf("%s requires a key and value argument", cmdType)
	}

	return saveEnvMeta(ctx, tracer, proxy, sandboxID, cmdMetadata, args[0], args[1])
}

func saveEnvMeta(
	ctx context.Context,
	tracer trace.Tracer,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	cmdMetadata sandboxtools.CommandMetadata,
	envName string,
	envValue string,
) (sandboxtools.CommandMetadata, error) {
	err := sandboxtools.RunCommandWithOutput(
		ctx,
		tracer,
		proxy,
		sandboxID,
		fmt.Sprintf(`printf "%s"`, envValue),
		sandboxtools.CommandMetadata{
			User: "root",
		},
		func(stdout, stderr string) {
			envValue = stdout
		},
	)
	if err != nil {
		return sandboxtools.CommandMetadata{}, fmt.Errorf("executing the environment variable %s: %w", envName, err)
	}

	envVars := maps.Clone(cmdMetadata.EnvVars)
	envVars[envName] = envValue
	cmdMetadata.EnvVars = envVars

	return cmdMetadata, nil
}
