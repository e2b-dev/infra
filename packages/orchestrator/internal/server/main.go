package server

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"sync"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/dns"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const ServiceName = "orchestrator"

type server struct {
	orchestrator.UnimplementedSandboxServiceServer
	sandboxes     *smap.Map[*sandbox.Sandbox]
	dns           *dns.DNS
	tracer        trace.Tracer
	networkPool   *network.Pool
	templateCache *template.Cache

	pauseMu sync.Mutex
}

type Service struct {
	server   *server
	grpc     *grpc.Server
	dns      *dns.DNS
	port     uint16
	shutdown struct {
		once sync.Once
		op   func(context.Context) error
		err  error
	}
}

// InterceptorLogger adapts zap logger to interceptor logger.
// This code is simple enough to be copied and not imported.
func InterceptorLogger(l *zap.Logger) logging.Logger {
	return logging.LoggerFunc(func(ctx context.Context, lvl logging.Level, msg string, fields ...any) {
		f := make([]zap.Field, 0, len(fields)/2)

		methodFullNameMap := map[string]string{
			"grpc.service":     "...",
			"grpc.method":      "...",
			"grpc.method_type": "...",
			"grpc.code":        "-",
		}

		for i := 0; i < len(fields); i += 2 {
			key := fields[i]
			value := fields[i+1]

			switch v := value.(type) {
			case string:
				f = append(f, zap.String(key.(string), v))

				_, ok := methodFullNameMap[key.(string)]
				if ok {
					methodFullNameMap[key.(string)] = v
				}
			case int:
				f = append(f, zap.Int(key.(string), v))
			case bool:
				f = append(f, zap.Bool(key.(string), v))
			default:
				f = append(f, zap.Any(key.(string), v))
			}
		}

		logger := l.WithOptions(zap.AddCallerSkip(1)).With(f...)

		methodFullName := fmt.Sprintf("%s/%s/%s",
			methodFullNameMap["grpc.service"],
			methodFullNameMap["grpc.method"],
			methodFullNameMap["grpc.method_type"],
		)
		if msg == "finished call" || msg == "finished streaming call" {
			methodFullName = fmt.Sprintf("%s [%s]", methodFullName, methodFullNameMap["grpc.code"])
		}
		message := fmt.Sprintf("%s: %s", methodFullName, msg)

		switch lvl {
		case logging.LevelDebug:
			logger.Debug(message)
		case logging.LevelInfo:
			logger.Info(message)
		case logging.LevelWarn:
			logger.Warn(message)
		case logging.LevelError:
			logger.Error(message)
		default:
			panic(fmt.Sprintf("unknown level %v", lvl))
		}
	})
}

func New(ctx context.Context, port uint) (*Service, error) {
	if port > math.MaxUint16 {
		return nil, fmt.Errorf("%d is larger than maximum possible port %d", port, math.MaxInt16)
	}

	srv := &Service{port: uint16(port)}

	templateCache, err := template.NewCache(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create template cache: %w", err)
	}

	networkPool, err := network.NewPool(ctx, network.NewSlotsPoolSize, network.ReusedSlotsPoolSize)
	if err != nil {
		return nil, fmt.Errorf("failed to create network pool: %w", err)
	}

	// BLOCK: initialize services
	{
		srv.dns = dns.New()

		opts := []logging.Option{
			logging.WithLogOnEvents(logging.StartCall, logging.PayloadReceived, logging.PayloadSent, logging.FinishCall),
			logging.WithLevels(logging.DefaultServerCodeToLevel),
			logging.WithFieldsFromContext(logging.ExtractFields),
		}
		srv.grpc = grpc.NewServer(
			grpc.StatsHandler(e2bgrpc.NewStatsWrapper(otelgrpc.NewServerHandler())),
			grpc.ChainUnaryInterceptor(
				recovery.UnaryServerInterceptor(),
				logging.UnaryServerInterceptor(InterceptorLogger(zap.L()), opts...),
			),
			grpc.ChainStreamInterceptor(
				logging.StreamServerInterceptor(InterceptorLogger(zap.L()), opts...),
			),
		)

		srv.server = &server{
			tracer:        otel.Tracer(ServiceName),
			dns:           srv.dns,
			sandboxes:     smap.New[*sandbox.Sandbox](),
			networkPool:   networkPool,
			templateCache: templateCache,
		}
	}

	orchestrator.RegisterSandboxServiceServer(srv.grpc, srv.server)
	grpc_health_v1.RegisterHealthServer(srv.grpc, health.NewServer())

	return srv, nil
}

// Start launches
func (srv *Service) Start(context.Context) error {
	if srv.server == nil || srv.dns == nil || srv.grpc == nil {
		return errors.New("orchestrator services are not initialized")
	}

	go func() {
		zap.L().Info("Starting DNS server")
		if err := srv.dns.Start("127.0.0.4", 53); err != nil {
			zap.L().Fatal("Failed running DNS server", zap.Error(err))
		}
	}()

	// the listener is closed by the shutdown operation
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", srv.port))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", srv.port, err)
	}

	zap.L().Info("Starting orchestrator server", zap.Uint16("port", srv.port))

	go func() {
		if err := srv.grpc.Serve(lis); err != nil {
			zap.L().Fatal("grpc server failed to serve", zap.Error(err))
		}
	}()

	srv.shutdown.op = func(ctx context.Context) error {
		var errs []error

		srv.grpc.GracefulStop()

		if err := lis.Close(); err != nil {
			errs = append(errs, err)
		}

		if err := srv.dns.Close(ctx); err != nil {
			errs = append(errs, err)
		}

		return errors.Join(errs...)
	}

	return nil
}

func (srv *Service) Close(ctx context.Context) error {
	srv.shutdown.once.Do(func() {
		if srv.shutdown.op == nil {
			// should only be true if there was an error
			// during startup.
			return
		}

		srv.shutdown.err = srv.shutdown.op(ctx)
		srv.shutdown.op = nil
	})
	return srv.shutdown.err
}
