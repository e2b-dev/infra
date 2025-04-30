package edge

import (
	"context"
	"fmt"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/api"
	configuration "github.com/e2b-dev/infra/packages/proxy/internal/edge/configurator"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/handlers"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/service-discovery"
	updater "github.com/e2b-dev/infra/packages/proxy/internal/edge/updater"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"net"
	"net/http"
	"os"
	"time"
)

type Service struct {
	serverPort  int
	server      *http.Server
	serverStore *handlers.APIStore

	logger *zap.Logger

	serviceDiscovery *service_discovery.ServiceDiscovery
	updater          *updater.Updater
}

const (
	serviceVersion = "v1.1.0"
	serviceType    = "edge"

	configSetupTimeout = 5 * time.Second
)

func NewService(ctx context.Context, logger *zap.Logger, proxyDrainingHandler func()) (*Service, error) {
	configAdapter, err := configuration.NewAutoConfigurationAdapter()
	if err != nil {
		return nil, err
	}

	configCtx, configCtxCancel := context.WithTimeout(ctx, configSetupTimeout)
	defer configCtxCancel()

	config, err := configAdapter.GetConfiguration(configCtx)
	if err != nil {
		return nil, err
	}

	opts, err := redis.ParseURL(config.RedisUrl)
	if err != nil {
		return nil, err
	}

	serviceId := uuid.NewString()
	serviceDiscovery := service_discovery.NewServiceDiscovery(
		&service_discovery.ServiceDiscoveryConfig{
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

	// todo: make it configurable
	updaterUrl := "https://e2b-eu-west-1-assets.s3.eu-west-1.amazonaws.com/edge-agent"
	updaterService := updater.NewUpdater(updaterUrl, logger)

	var serverStore *handlers.APIStore
	var server *http.Server

	selfUpdateHandler := func() updater.UpdaterResponse {
		logger.Info("self update handler called")

		if updaterService == nil {
			err := fmt.Errorf("service updater is not configured")
			return updater.UpdaterResponse{
				Success: false,
				Message: err.Error(),
				Error:   err,
			}
		}

		updateCtx, updateCtxCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer updateCtxCancel()

		// let's check if its possible to update
		// todo: add some handling of current version in some catalog or something
		updateResp := updaterService.Update(updateCtx, nil)
		if !updateResp.Success {
			return updateResp
		}

		// let's make service as draining and start shutdown process
		serviceDiscovery.SetStatus(service_discovery.StatusDraining)

		go func() {
			// wait for services to realize we are unhealthy)
			if !env.IsLocal() {
				time.Sleep(30 * time.Second)
			}

			// start draining of http proxy
			proxyDrainingHandler()

			serviceDiscovery.SetStatus(service_discovery.StatusUnhealthy)
			serverStore.SetHealth(service_discovery.StatusUnhealthy)

			// wait for services to realize we are unhealthy
			// mainly just for in-process requests etc
			time.Sleep(15 * time.Second)

			// shutdown edge http server
			shutdownCtx, shutdownCtxCancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer shutdownCtxCancel()

			err := server.Shutdown(shutdownCtx)
			if err != nil {
				logger.Error("failed to shutdown server", zap.Error(err))
			}

			// todo: this should be done carefully in service main
			os.Exit(0)
		}()

		// todo: shutdown process must be handled async because we are still in-request
		return updater.UpdaterResponse{
			Success: true,
		}
	}

	selfDrainHandler := func() error {
		logger.Info("self drain handler called")
		serviceDiscovery.SetStatus(service_discovery.StatusDraining)

		// let's make service as draining and start shutdown process
		serviceDiscovery.SetStatus(service_discovery.StatusDraining)

		go func() {
			// wait for services to realize we are unhealthy)
			if !env.IsLocal() {
				time.Sleep(30 * time.Second)
			}

			// start draining of http proxy
			proxyDrainingHandler()

			serviceDiscovery.SetStatus(service_discovery.StatusUnhealthy)
			serverStore.SetHealth(service_discovery.StatusUnhealthy)

			// wait for services to realize we are unhealthy
			// mainly just for in-process requests etc
			time.Sleep(15 * time.Second)

			// shutdown edge http server
			shutdownCtx, shutdownCtxCancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer shutdownCtxCancel()

			err := server.Shutdown(shutdownCtx)
			if err != nil {
				logger.Error("failed to shutdown server", zap.Error(err))
			}

			// we wil just wait for scaling manager to kill whole instance
			// because otherwise we will be just started again with systemd manager
		}()

		return nil
	}

	serverStore, err = handlers.NewStore(serviceDiscovery, logger, &selfUpdateHandler, &selfDrainHandler)
	if err != nil {
		return nil, err
	}

	serverSwagger, err := api.GetSwagger()
	if err != nil {
		return nil, err
	}

	server = NewGinServer(ctx, logger, serverStore, config.ServicePort, serverSwagger)

	return &Service{
		serverPort:  config.ServicePort,
		serverStore: serverStore,
		server:      server,

		logger: logger,

		updater:          updaterService,
		serviceDiscovery: serviceDiscovery,
	}, nil
}

func (s *Service) Start() error {
	zap.L().Info("starting edge service", zap.Int("port", s.serverPort))

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.serverPort))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", s.serverPort, err)
	}

	if err := s.server.Serve(lis); err != nil {
		println("edge server err")
		return err
	}

	return nil
}

func (s *Service) Shutdown(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}

	return nil
}

func (s *Service) StartServiceDiscovery(ctx context.Context) {
	go func() { s.serviceDiscovery.StartSelfRegistration(ctx) }()
}
