package commands

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/buildcontext"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/commands")

type CommandExecutor struct {
	buildcontext.BuildContext

	buildStorage storage.StorageProvider
	proxy        *proxy.SandboxProxy
}

func NewCommandExecutor(
	buildContext buildcontext.BuildContext,
	buildStorage storage.StorageProvider,
	proxy *proxy.SandboxProxy,
) *CommandExecutor {
	return &CommandExecutor{
		BuildContext: buildContext,

		buildStorage: buildStorage,
		proxy:        proxy,
	}
}

func (ce *CommandExecutor) getCommand(
	step *templatemanager.TemplateStep,
) (Command, error) {
	cmdType := strings.ToUpper(step.GetType())

	var cmd Command
	switch cmdType {
	case "ADD", "COPY":
		cmd = &Copy{
			FilesStorage: ce.buildStorage,
			CacheScope:   ce.CacheScope,
		}
	case "RUN":
		cmd = &Run{}
	case "USER":
		cmd = &User{}
	case "WORKDIR":
		cmd = &Workdir{}
	case "ENV", "ARG":
		cmd = &Env{}
	}

	if cmd == nil {
		return nil, fmt.Errorf("command type %s is not implemented", cmdType)
	}

	return cmd, nil
}

func (ce *CommandExecutor) Execute(
	ctx context.Context,
	userLogger *zap.Logger,
	sbx *sandbox.Sandbox,
	prefix string,
	step *templatemanager.TemplateStep,
	cmdMetadata metadata.Context,
) (metadata.Context, error) {
	ctx, span := tracer.Start(ctx, "apply-command", trace.WithAttributes(
		attribute.String("prefix", prefix),
		attribute.String("sandbox.id", sbx.Runtime.SandboxID),
		attribute.String("step.type", step.GetType()),
		attribute.StringSlice("step.args", step.GetArgs()),
		attribute.String("step.files.hash", utils.Sprintp(step.FilesHash)), //nolint:protogetter // we need the nil check too
	))
	defer span.End()

	cmd, err := ce.getCommand(step)
	if err != nil {
		return metadata.Context{}, fmt.Errorf("failed to get command for step %s: %w", step.GetType(), err)
	}

	cmdMetadata, err = cmd.Execute(
		ctx,
		userLogger,
		ce.proxy,
		sbx.Runtime.SandboxID,
		prefix,
		step,
		cmdMetadata,
	)
	if err != nil {
		return metadata.Context{}, err
	}
	return cmdMetadata, nil
}
