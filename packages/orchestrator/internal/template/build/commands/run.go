package commands

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

type Run struct{}

var _ Command = (*Run)(nil)

func (r *Run) Execute(
	ctx context.Context,
	tracer trace.Tracer,
	logger *zap.Logger,
	proxy *proxy.SandboxProxy,
	sandboxID string,
	prefix string,
	step *templatemanager.TemplateStep,
	cmdMetadata metadata.Context,
) (metadata.Context, error) {
	args := step.Args
	// args: [command optional_user]
	if len(args) < 1 {
		return metadata.Context{}, fmt.Errorf("RUN requires command argument")
	}

	originalMetadata := cmdMetadata

	// If a custom command user is specified, use it
	if len(args) >= 2 {
		cmdMetadata.User = args[1]
	}

	cmd := args[0]
	err := sandboxtools.RunCommandWithLogger(
		ctx,
		tracer,
		proxy,
		logger,
		zapcore.InfoLevel,
		prefix,
		sandboxID,
		cmd,
		cmdMetadata,
	)
	if err != nil {
		return metadata.Context{}, fmt.Errorf("failed to run command: %w", err)
	}

	return originalMetadata, nil
}
