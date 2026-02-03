package hyperloopserver

import (
	"context"
	"fmt"
	"net"
	"net/http"

	limits "github.com/gin-contrib/size"
	"github.com/gin-gonic/gin"
	middleware "github.com/oapi-codegen/gin-middleware"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/hyperloopserver/contracts"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/hyperloopserver/handlers"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const maxUploadLimit = 1 << 28 // 256 MiB

func NewHyperloopServer(ctx context.Context, port uint16, logger logger.Logger, sandboxes *sandbox.Map) (*http.Server, error) {
	sandboxCollectorAddr := env.LogsCollectorAddress()
	store := handlers.NewHyperloopStore(logger, sandboxes, sandboxCollectorAddr)
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
		Addr:    fmt.Sprintf("0.0.0.0:%d", port),

		BaseContext: func(net.Listener) context.Context { return ctx },
	}

	contracts.RegisterHandlersWithOptions(engine, store, contracts.GinServerOptions{})

	return server, nil
}
