package hyperloopserver

import (
	"context"
	"fmt"
	"net"
	"net/http"

	limits "github.com/gin-contrib/size"
	"github.com/gin-gonic/gin"
	middleware "github.com/oapi-codegen/gin-middleware"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/hyperloopserver/contracts"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/hyperloopserver/handlers"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const maxUploadLimit = 1 << 28 // 256 MiB

func NewHyperloopServer(ctx context.Context, config cfg.Config, logger logger.Logger, sandboxes *sandbox.Map) (*http.Server, error) {
	store := handlers.NewHyperloopStore(logger, sandboxes, config)
	swagger, err := contracts.GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("error getting swagger spec: %w", err)
	}

	engine := gin.New()
	engine.Use(
		gin.Recovery(),
		limits.RequestSizeLimiter(maxUploadLimit),
		middleware.OapiRequestValidatorWithOptions(swagger, &middleware.Options{}),
	)

	server := &http.Server{
		Handler: engine,
		Addr:    fmt.Sprintf("0.0.0.0:%d", config.NetworkConfig.HyperloopProxyPort),

		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	contracts.RegisterHandlersWithOptions(engine, store, contracts.GinServerOptions{})

	return server, nil
}
