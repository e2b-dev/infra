package commands

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

type Workdir struct{}

var _ Command = (*Workdir)(nil)

func (w *Workdir) Execute(
	ctx context.Context,
	logger *zap.Logger,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	prefix string,
	step *templatemanager.TemplateStep,
	cmdMetadata metadata.Context,
) (metadata.Context, error) {
	args := step.Args
	// args: [path]
	if len(args) < 1 {
		return metadata.Context{}, fmt.Errorf("WORKDIR requires a path argument")
	}

	workdirArg := args[0]

	err := sandboxtools.RunCommandWithLogger(
		ctx,
		proxy,
		logger,
		zapcore.InfoLevel,
		prefix,
		sandboxID,
		fmt.Sprintf(`mkdir -p "%s"`, workdirArg),
		metadata.Context{
			User:    cmdMetadata.User,
			EnvVars: cmdMetadata.EnvVars,
		},
	)
	if err != nil {
		return metadata.Context{}, fmt.Errorf("failed to create workdir: %w", err)
	}

	return saveWorkdirMeta(ctx, proxy, sandboxID, cmdMetadata, workdirArg)
}

func saveWorkdirMeta(
	ctx context.Context,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	cmdMetadata metadata.Context,
	workdir string,
) (metadata.Context, error) {
	err := sandboxtools.RunCommandWithOutput(
		ctx,
		proxy,
		sandboxID,
		fmt.Sprintf(`printf "%s"`, workdir),
		metadata.Context{
			User: "root",
		},
		func(stdout, stderr string) {
			workdir = stdout
		},
	)
	if err != nil {
		return metadata.Context{}, fmt.Errorf("failed to save workdir %s: %w", workdir, err)
	}

	cmdMetadata.WorkDir = &workdir
	return cmdMetadata, nil
}
