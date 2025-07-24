package command

import (
	"context"

	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
)

type Command interface {
	Execute(
		ctx context.Context,
		tracer trace.Tracer,
		postProcessor *writer.PostProcessor,
		proxy *proxy.SandboxProxy,
		sandboxID string,
		prefix string,
		step *templatemanager.TemplateStep,
		cmdMetadata sandboxtools.CommandMetadata,
	) (sandboxtools.CommandMetadata, error)
}
