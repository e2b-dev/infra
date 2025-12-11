package ioc

import (
	"net/http"

	"go.uber.org/fx"
	"google.golang.org/grpc"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/service"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/server"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type StaticDeps struct {
	Config           cfg.Config
	DevicePool       *nbd.DevicePool
	GRPCServer       *grpc.Server
	HealthHTTPServer *http.Server
	HyperloopServer  *http.Server
	NetworkPool      *network.Pool
	SandboxFactory   *sandbox.Factory
	SandboxProxy     *proxy.SandboxProxy
	ServiceInfo      *service.ServiceInfo
	TemplateManager  *server.ServerStore
	TracedLogger     logger.Logger
	VersionInfo      VersionInfo
}

type VersionInfo struct {
	Version string
	Commit  string
}

func build(deps StaticDeps) []fx.Option {
	opts := []fx.Option{
		fx.Supply(
			deps.Config,
			deps.DevicePool,
			deps.GRPCServer,
			HealthHTTPServer{
				deps.HealthHTTPServer,
			},
			HyperloopServer{
				deps.HyperloopServer,
			},
			deps.NetworkPool,
			asCMUXWaitBeforeShutdown(
				deps.SandboxFactory,
			),
			deps.SandboxProxy,
			deps.ServiceInfo,
			deps.TracedLogger,
			deps.VersionInfo,
		),

		fx.Invoke(
			startDevicePool,
			startHyperloopServer,
			startNetworkPool,
			startSandboxProxy,
			withCMUXWaitBeforeShutdown(
				startCMUXServer,
			),
		),
	}

	if deps.TemplateManager != nil {
		opts = append(opts, fx.Supply(
			asCMUXWaitBeforeShutdown(
				deps.TemplateManager,
			),
		))
	}

	return opts
}

func Validate(deps StaticDeps) error {
	options := build(deps)

	return fx.ValidateApp(options...)
}

func New(deps StaticDeps, opts ...fx.Option) *fx.App {
	options := build(deps)

	options = append(options, opts...)

	return fx.New(options...)
}
