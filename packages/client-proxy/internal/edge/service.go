package edge

import (
	"context"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/proxy/internal/configurator"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/handlers"
	"github.com/e2b-dev/infra/packages/proxy/internal/service-discovery"
)

const (
	serviceVersion = "v2.0.0"
	serviceType    = "edge"

	configSetupTimeout = 5 * time.Second
)

func NewEdgeAPIStore(ctx context.Context, logger *zap.Logger, tracer trace.Tracer, drainingHandler *func(terminate bool)) (*handlers.APIStore, error) {
	configAdapter, err := configuration.NewConfigurationAdapter()
	if err != nil {
		return nil, err
	}

	configCtx, configCtxCancel := context.WithTimeout(ctx, configSetupTimeout)
	defer configCtxCancel()

	config, err := configAdapter.GetConfiguration(configCtx)
	if err != nil {
		return nil, err
	}

	//opts, err := redis.ParseURL(config.RedisUrl)
	//if err != nil {
	//	return nil, err
	//}

	serviceId := uuid.NewString()
	serviceDiscovery := service_discovery.NewDnsServiceDiscovery(
		&service_discovery.DnsServiceDiscoveryConfig{
			Logger: logger,

			NodePort: config.ServicePort,
			NodeIp:   config.ServiceIpv4,

			ServiceId:      serviceId,
			ServiceType:    serviceType,
			ServiceVersion: serviceVersion,
			ServiceStatus:  service_discovery.StatusHealthy,

			// todo: this should be ideally taken from some ENV
			OrchestratorsDomain: "orchestrator.service.consul",
			OrchestratorsPort:   5008,
		},
	)

	/*
		serviceDiscovery := service_discovery.NewRedisServiceDiscovery(
			ctx,
			&service_discovery.RedisServiceDiscoveryConfig{
				RedisClient: redis.NewClient(opts),
				Logger:      logger,

				NodePort: config.ServicePort,
				NodeIp:   config.ServiceIpv4,

				ServiceId:      serviceId,
				ServiceType:    serviceType,
				ServiceVersion: serviceVersion,
				ServiceStatus:  service_discovery.StatusHealthy,
			},
		)
	*/

	selfDrainHandler := func() error {
		(*drainingHandler)(false) // program should stay alive and terminated whole instance
		return nil
	}

	selfUpdateHandler := func() error {
		(*drainingHandler)(true) // we want to restart service after update
		panic("not implemented")
	}

	store, err := handlers.NewStore(ctx, serviceDiscovery, logger, tracer, &selfUpdateHandler, &selfDrainHandler)
	if err != nil {
		return nil, err
	}

	return store, nil
}
