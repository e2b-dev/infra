package edge

import (
	"context"

	"go.opentelemetry.io/otel"

	"github.com/e2b-dev/infra/packages/proxy/internal/cfg"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/handlers"
	e2binfo "github.com/e2b-dev/infra/packages/proxy/internal/edge/info"
	e2borchestrators "github.com/e2b-dev/infra/packages/proxy/internal/edge/pool"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	catalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/client-proxy/internal/edge")

func NewEdgeAPIStore(
	ctx context.Context,
	l logger.Logger,
	info *e2binfo.ServiceInfo,
	edges *e2borchestrators.EdgePool,
	orchestrators *e2borchestrators.OrchestratorsPool,
	catalog catalog.SandboxesCatalog,
	config cfg.Config,
) (*handlers.APIStore, error) {
	store, err := handlers.NewStore(ctx, l, info, orchestrators, edges, catalog, config)
	if err != nil {
		return nil, err
	}

	return store, nil
}
