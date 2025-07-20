package command

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

type Run struct{}

func (r *Run) Execute(
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
	args := step.Args
	// args: command and args, e.g., ["sh", "-c", "echo hi"]
	if len(args) < 1 {
		return fmt.Errorf("RUN requires command arguments")
	}

	cmd := strings.Join(args, " ")
	return sandboxtools.RunCommandWithLogger(
		ctx,
		tracer,
		proxy,
		postProcessor,
		zapcore.InfoLevel,
		prefix,
		sandboxID,
		cmd,
		cmdMetadata,
	)
}
