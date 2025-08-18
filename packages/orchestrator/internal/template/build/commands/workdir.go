package commands

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

type Workdir struct{}

func (w *Workdir) Execute(
	ctx context.Context,
	tracer trace.Tracer,
	postProcessor *writer.PostProcessor,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	prefix string,
	step *templatemanager.TemplateStep,
	cmdMetadata metadata.CommandMetadata,
) (metadata.CommandMetadata, error) {
	args := step.Args
	// args: [path]
	if len(args) < 1 {
		return metadata.CommandMetadata{}, fmt.Errorf("WORKDIR requires a path argument")
	}

	workdirArg := args[0]

	err := sandboxtools.RunCommandWithLogger(
		ctx,
		tracer,
		proxy,
		postProcessor,
		zapcore.InfoLevel,
		prefix,
		sandboxID,
		fmt.Sprintf(`mkdir -p "%s"`, workdirArg),
		metadata.CommandMetadata{
			User:    cmdMetadata.User,
			EnvVars: cmdMetadata.EnvVars,
		},
	)
	if err != nil {
		return metadata.CommandMetadata{}, fmt.Errorf("failed to create workdir: %w", err)
	}

	return saveWorkdirMeta(ctx, tracer, proxy, sandboxID, cmdMetadata, workdirArg)
}

func saveWorkdirMeta(
	ctx context.Context,
	tracer trace.Tracer,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	cmdMetadata metadata.CommandMetadata,
	workdir string,
) (metadata.CommandMetadata, error) {
	err := sandboxtools.RunCommandWithOutput(
		ctx,
		tracer,
		proxy,
		sandboxID,
		fmt.Sprintf(`printf "%s"`, workdir),
		metadata.CommandMetadata{
			User: "root",
		},
		func(stdout, stderr string) {
			workdir = stdout
		},
	)
	if err != nil {
		return metadata.CommandMetadata{}, fmt.Errorf("failed to save workdir %s: %w", workdir, err)
	}

	cmdMetadata.WorkDir = &workdir
	return cmdMetadata, nil
}
