package command

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

var envPathPrefix = filepath.Join(cmdMetadataBaseDirPath, "env")

type Env struct{}

func (e *Env) Execute(
	ctx context.Context,
	tracer trace.Tracer,
	postProcessor *writer.PostProcessor,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	templateID string,
	prefix string,
	step *templatemanager.TemplateStep,
	cmdMetadata sandboxtools.CommandMetadata,
) error {
	cmdType := strings.ToUpper(step.Type)
	args := step.Args
	// args: [key value]
	if len(args) < 2 {
		return fmt.Errorf("%s requires a key and value argument", cmdType)
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
) error {
	envPath := fmt.Sprintf("%s/%s", envPathPrefix, cmdMetadata.User)

	return sandboxtools.RunCommand(
		ctx,
		tracer,
		proxy,
		sandboxID,
		fmt.Sprintf(`mkdir -p "$(dirname "%s")" && echo "%s=%s" >> "%s"`, envPath, envName, envValue, envPath),
		sandboxtools.CommandMetadata{
			User: "root",
		},
	)
}
