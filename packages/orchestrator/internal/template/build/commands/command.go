package commands

import (
	"context"

	"go.uber.org/zap/zapcore"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type Command interface {
	Execute(
		ctx context.Context,
		logger logger.Logger,
		lvl zapcore.Level,
		proxy *proxy.SandboxProxy,
		sandboxID string,
		prefix string,
		step *templatemanager.TemplateStep,
		cmdMetadata metadata.Context,
	) (metadata.Context, error)
}
