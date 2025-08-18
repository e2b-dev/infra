package commands

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/buildcontext"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type CommandExecutor struct {
	buildcontext.BuildContext

	tracer trace.Tracer

	buildStorage storage.StorageProvider
	proxy        *proxy.SandboxProxy
}

func NewCommandExecutor(
	buildContext buildcontext.BuildContext,
	tracer trace.Tracer,
	buildStorage storage.StorageProvider,
	proxy *proxy.SandboxProxy,
) *CommandExecutor {
	return &CommandExecutor{
		BuildContext: buildContext,

		tracer: tracer,

		buildStorage: buildStorage,
		proxy:        proxy,
	}
}

func (ce *CommandExecutor) getCommand(
	step *templatemanager.TemplateStep,
) (Command, error) {
	cmdType := strings.ToUpper(step.Type)

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
	sbx *sandbox.Sandbox,
	prefix string,
	step *templatemanager.TemplateStep,
	cmdMetadata metadata.CommandMetadata,
) (metadata.CommandMetadata, error) {
	ctx, span := ce.tracer.Start(ctx, "apply-command", trace.WithAttributes(
		attribute.String("prefix", prefix),
		attribute.String("sandbox.id", sbx.Runtime.SandboxID),
		attribute.String("step.type", step.Type),
		attribute.StringSlice("step.args", step.Args),
		attribute.String("step.files.hash", utils.Sprintp(step.FilesHash)),
	))
	defer span.End()

	cmd, err := ce.getCommand(step)
	if err != nil {
		return metadata.CommandMetadata{}, fmt.Errorf("failed to get command for step %s: %w", step.Type, err)
	}

	cmdMetadata, err = cmd.Execute(
		ctx,
		ce.tracer,
		ce.UserLogger,
		ce.proxy,
		sbx.Runtime.SandboxID,
		prefix,
		step,
		cmdMetadata,
	)
	if err != nil {
		return metadata.CommandMetadata{}, err
	}
	return cmdMetadata, nil
}
