package ioc

import (
	"go.uber.org/fx"
	"google.golang.org/grpc"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/factories"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/service"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const grpcRegisterableGroupTag = `group:"grpc-registerables"`

type grpcRegisterable struct {
	fn func(*grpc.Server)
}

func (g grpcRegisterable) Register(s *grpc.Server) {
	g.fn(s)
}

type GRPCServerRegistrar interface {
	Register(s *grpc.Server)
}

func asGRPCRegisterable(f any) any {
	return fx.Annotate(
		f,
		fx.As((*GRPCServerRegistrar)(nil)),
		fx.ResultTags(grpcRegisterableGroupTag),
	)
}

func withGRPCRegisterables(f any) any {
	return fx.Annotate(
		f,
		fx.ParamTags(grpcRegisterableGroupTag),
	)
}

func newGRPCModule() fx.Option {
	return fx.Module("grpc",
		fx.Provide(
			asGRPCRegisterable(newInfoService),
			withGRPCRegisterables(newGRPCServer),
		),
	)
}

func newInfoService(
	sandboxes *sandbox.Map,
	serviceInfo *service.ServiceInfo,
) grpcRegisterable {
	s := service.NewInfoService(serviceInfo, sandboxes)

	return grpcRegisterable{func(g *grpc.Server) { orchestratorinfo.RegisterInfoServiceServer(g, s) }}
}

func newGRPCServer(registerables []GRPCServerRegistrar, tel *telemetry.Client) *grpc.Server {
	s := factories.NewGRPCServer(tel)

	for _, r := range registerables {
		r.Register(s)
	}

	return s
}
