package hyperloopserver

import (
	"context"
	"fmt"
	"net"
	"net/http"

	limits "github.com/gin-contrib/size"
	"github.com/gin-gonic/gin"
	middleware "github.com/oapi-codegen/gin-middleware"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/hyperloopserver/handlers"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	api "github.com/e2b-dev/infra/packages/shared/pkg/http/hyperloop"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const maxUploadLimit = 1 << 28 // 256 MiB

func NewHyperloopServer(ctx context.Context, port uint, logger *zap.Logger, sandboxes *smap.Map[*sandbox.Sandbox]) (*http.Server, error) {
	sandboxCollectorAddr := env.LogsCollectorAddress()
	store := handlers.NewHyperloopStore(logger, sandboxes, sandboxCollectorAddr)
	swagger, err := api.GetSwagger()
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
		Addr:    fmt.Sprintf("0.0.0.0:%d", port),

		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	api.RegisterHandlersWithOptions(engine, store, api.GinServerOptions{})

	return server, nil
}
