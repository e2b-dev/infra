package edge

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/proxy/internal"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/api"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/handlers"
	e2binfo "github.com/e2b-dev/infra/packages/proxy/internal/edge/info"
	e2borchestrators "github.com/e2b-dev/infra/packages/proxy/internal/edge/pool"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/sandboxes"
	"github.com/e2b-dev/infra/packages/proxy/internal/service-discovery"
)

func NewEdgeAPIStore(
	ctx context.Context,
	logger *zap.Logger,
	tracer trace.Tracer,
	serviceCommit string,
	serviceVersion string,
	edgeSD service_discovery.ServiceDiscoveryAdapter,
	orchestrators *e2borchestrators.OrchestratorsPool,
	catalog *sandboxes.SandboxesCatalog,
) (*handlers.APIStore, error) {
	edgePort := internal.GetEdgeServicePort()
	info := &e2binfo.ServiceInfo{
		NodeId:        internal.GetNodeID(),
		ServiceId:     uuid.NewString(),
		SourceVersion: serviceVersion,
		SourceCommit:  serviceCommit,
		Startup:       time.Now(),
		Host:          fmt.Sprintf("%s:%d", internal.GetNodeIP(), edgePort),
	}

	// todo: we should with health status until service discovery is resolved so we are not returning empty lists
	info.SetStatus(api.Healthy)

	edgePool := e2borchestrators.NewEdgePool(ctx, logger, edgeSD, tracer, info.Host)
	store, err := handlers.NewStore(ctx, logger, tracer, info, orchestrators, edgePool, catalog)
	if err != nil {
		return nil, err
	}

	return store, nil
}
