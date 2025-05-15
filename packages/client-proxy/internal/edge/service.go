package edge

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/proxy/internal"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/api"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/handlers"
	e2binfo "github.com/e2b-dev/infra/packages/proxy/internal/edge/info"
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

	_, _, err := service_discovery.NewServiceDiscoveryProvider(ctx, edgePort, orchestratorPort, logger)
	if err != nil {
		return nil, err
	}

	info := &e2binfo.ServiceInfo{
		NodeId:        internal.GetNodeID(),
		ServiceId:     uuid.NewString(),
		SourceVersion: serviceVersion,
		SourceCommit:  serviceCommit,
		Startup:       time.Now(),
	}
	info.SetStatus(api.Healthy)

	// todo
	// orchestrator pool
	// edge pool

	store, err := handlers.NewStore(ctx, logger, tracer, info, &selfUpdateHandler, &selfDrainHandler)
	if err != nil {
		return nil, err
	}

	return store, nil
}
