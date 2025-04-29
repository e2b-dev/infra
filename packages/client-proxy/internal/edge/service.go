package edge

import (
	"context"
	"fmt"
	configuration "github.com/e2b-dev/infra/packages/proxy/internal/edge/configurator"
	edge "github.com/e2b-dev/infra/packages/shared/pkg/grpc/edge"
	"github.com/go-redsync/redsync/v4"
	"github.com/go-redsync/redsync/v4/redis/goredis/v9"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"net"
	"os"
	"time"
)

type Service struct {
	grpcPort         int
	grpc             *grpc.Server
	edgeServer       *edgeServer
	healthServer     *HealthServer
	distributedMutex *redsync.Mutex
	serviceDiscovery *serviceDiscovery
}

const (
	configSetupTimeout = 5 * time.Second
)

func Run(logger *zap.Logger, healthServerPort int, ctx context.Context) error {
	service, err := NewService(ctx, healthServerPort)
	if err != nil {
		logger.Error("failed to create service", zap.Error(err))
		return err
	}

	errorChan := make(chan error)

	go func() {
		err := service.Start()
		if err != nil {
			logger.Error("failed to start edge service", zap.Error(err))
			errorChan <- err
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("context done, shutting down edge service")
		service.Shutdown()
		return nil
	case err := <-errorChan:
		if err != nil {
			logger.Error("error in edge service", zap.Error(err))
			return err
		}
	}

	return nil
}

func NewService(ctx context.Context, healthServerPort int) (*Service, error) {
	configAdapter, err := configuration.NewAutoConfigurationAdapter()
	if err != nil {
		return nil, err
	}

	configCtx, confixCtxCancel := context.WithTimeout(ctx, configSetupTimeout)
	defer confixCtxCancel()

	config, err := configAdapter.GetConfiguration(configCtx)
	if err != nil {
		return nil, err
	}

	opts, err := redis.ParseURL(config.RedisReaderUrl)
	if err != nil {
		return nil, err
	}

	// todo: this is just temporary, we need to use the config adapter
	if config.SelfUpdateSourceUrl != nil {
		go autoUpdate(*config.SelfUpdateSourceUrl, 10*time.Second)
	}

	healthServer := NewHealthServer(healthServerPort, zap.L())

	go func() {
		err := healthServer.Start()
		if err != nil {
			zap.L().Error("failed to start health server", zap.Error(err))
			os.Exit(1) // todo: handle properly
			return
		}
	}()

	//grpcOpts := []logging.Option{
	//	logging.WithLogOnEvents(logging.StartCall, logging.PayloadReceived, logging.PayloadSent, logging.FinishCall),
	//	logging.WithLevels(logging.DefaultServerCodeToLevel),
	//	logging.WithFieldsFromContext(logging.ExtractFields),
	//}

	grpcServer := grpc.NewServer(
	/*
		grpc.StatsHandler(e2bgrpc.NewStatsWrapper(otelgrpc.NewServerHandler())),
		grpc.ChainUnaryInterceptor(
			recovery.UnaryServerInterceptor(),
			selector.UnaryServerInterceptor(
				logging.UnaryServerInterceptor(logger.GRPCLogger(zap.L()), grpcOpts...),
				logger.WithoutHealthCheck(),
			),
		),
		grpc.ChainStreamInterceptor(
			selector.StreamServerInterceptor(
				logging.StreamServerInterceptor(logger.GRPCLogger(zap.L()), grpcOpts...),
				logger.WithoutHealthCheck(),
			),
		),*/
	)

	redisClient := redis.NewClient(opts)

	rsPool := goredis.NewPool(redisClient)
	rs := redsync.New(rsPool)

	// Obtain a new mutex by using the same name for all instances wanting the
	// same mx.
	mutexName := "my-global-mutex" // todo: constant
	mutex := rs.NewMutex(mutexName)

	return &Service{
		grpcPort: config.ServicePort,
		grpc:     grpcServer,

		healthServer: healthServer,
		edgeServer: &edgeServer{
			redisClient: redisClient,
			healthy:     true,
			mx:          mutex,
		},

		serviceDiscovery: newServiceDiscovery(redisClient),
		distributedMutex: mutex,
	}, nil
}

func (s *Service) Start() error {
	zap.L().Info("starting edge service", zap.Int("port", s.grpcPort))

	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.grpcPort))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", s.grpcPort, err)
	}

	edge.RegisterEdgeServiceServer(s.grpc, s.edgeServer)

	// todo: move this to a separate function
	go func() {
		for {
			registerCtx := context.Background()
			registerErr := s.serviceDiscovery.registerMyself(registerCtx)
			if registerErr != nil {
				zap.L().Error("failed to register myself", zap.Error(registerErr))
			} else {
				zap.L().Info("registered myself")
			}

			time.Sleep(5 * time.Second)
		}

	}()

	if err := s.grpc.Serve(lis); err != nil {
		println("edge server err")
		return err
	}

	return nil
}

func (s *Service) Shutdown() {
	if s.grpc != nil {
		s.grpc.GracefulStop()
	}
}
