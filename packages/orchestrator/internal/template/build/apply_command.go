package build

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/command"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/utils"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

func (b *Builder) getCommand(
	step *templatemanager.TemplateStep,
) (command.Command, error) {
	cmdType := strings.ToUpper(step.Type)

	var cmd command.Command
	switch cmdType {
	case "ADD", "COPY":
		cmd = &command.Copy{
			FilesStorage: b.buildStorage,
		}
	case "RUN":
		cmd = &command.Run{}
	case "USER":
		cmd = &command.User{}
	case "WORKDIR":
		cmd = &command.Workdir{}
	case "ENV", "ARG":
		cmd = &command.Env{}
	}

	if cmd == nil {
		return nil, fmt.Errorf("command type %s is not implemented", cmdType)
	}

	return cmd, nil
}

func (b *Builder) applyCommand(
	ctx context.Context,
	postProcessor *writer.PostProcessor,
	templateID string,
	sbx *sandbox.Sandbox,
	prefix string,
	step *templatemanager.TemplateStep,
	baseCmdMetadata sandboxtools.CommandMetadata,
) error {
	ctx, span := b.tracer.Start(ctx, "apply-command", trace.WithAttributes(
		attribute.String("prefix", prefix),
		attribute.String("sandbox.id", sbx.Metadata.Config.SandboxId),
		attribute.String("step.type", step.Type),
		attribute.StringSlice("step.args", step.Args),
		attribute.String("step.files.hash", utils.Sprintp(step.FilesHash)),
	))
	defer span.End()

	cmdMetadata, err := command.ReadCommandMetadata(ctx, b.tracer, b.proxy, sbx.Metadata.Config.SandboxId, baseCmdMetadata)
	if err != nil {
		return fmt.Errorf("failed to read command metadata: %w", err)
	}

	cmd, err := b.getCommand(step)
	if err != nil {
		return fmt.Errorf("failed to get command for step %s: %w", step.Type, err)
	}

	err = cmd.Execute(
		ctx,
		b.tracer,
		postProcessor,
		b.proxy,
		sbx.Config.SandboxId,
		templateID,
		prefix,
		step,
		cmdMetadata,
	)
	if err != nil {
		return fmt.Errorf("failed to execute command: %w", err)
	}
	return nil
}
