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
	"github.com/e2b-dev/infra/packages/proxy/internal/service-discovery"
)

func NewEdgeAPIStore(ctx context.Context, logger *zap.Logger, tracer trace.Tracer, serviceCommit string, serviceVersion string, drainingHandler *func(terminate bool)) (*handlers.APIStore, error) {
	selfDrainHandler := func() error {
		(*drainingHandler)(false) // program should stay alive and terminated whole instance
		return nil
	}

	selfUpdateHandler := func() error {
		(*drainingHandler)(true) // we want to restart service after update
		panic("not implemented")
	}

	edgePort := internal.GetEdgeServicePort()
	orchestratorPort := internal.GetOrchestratorServicePort()

	edgeSD, orchestratorsSD, err := service_discovery.NewServiceDiscoveryProvider(ctx, edgePort, orchestratorPort, logger)
	if err != nil {
		return nil, err
	}

	info := &e2binfo.ServiceInfo{
		NodeId:        internal.GetNodeID(),
		ServiceId:     uuid.NewString(),
		SourceVersion: serviceVersion,
		SourceCommit:  serviceCommit,
		Startup:       time.Now(),
		Host:          fmt.Sprintf("%s:%d", internal.GetNodeIP(), edgePort),
	}
	info.SetStatus(api.Healthy)

	// todo
	// edge pool
	orchestratorsPool := e2borchestrators.NewOrchestratorsPool(ctx, logger, orchestratorsSD, tracer)

	store, err := handlers.NewStore(ctx, logger, tracer, info, orchestratorsPool, edgeSD, &selfUpdateHandler, &selfDrainHandler)
	if err != nil {
		return nil, err
	}

	return store, nil
}
