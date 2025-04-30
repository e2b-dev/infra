package edge

import (
	"context"
	"fmt"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/api"
	"go.uber.org/zap"
	"net"
	"net/http"
	"time"

	limits "github.com/gin-contrib/size"
	middleware "github.com/oapi-codegen/gin-middleware"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/proxy/internal/edge/handlers"
)

const (
	maxMultipartMemory = 1 << 27 // 128 MiB
	maxUploadLimit     = 1 << 28 // 256 MiB

	maxReadHeaderTimeout = 60 * time.Second
	maxReadTimeout       = 75 * time.Second
	maxWriteTimeout      = 75 * time.Second
)

func NewGinServer(ctx context.Context, logger *zap.Logger, apiStore *handlers.APIStore, apiPort int, swagger *openapi3.T) *http.Server {
	// Clear out the servers array in the swagger spec, that skips validating
	// that server names match. We don't know how this thing will be run.
	swagger.Servers = nil

	handler := gin.New()

	// todo: setup CORS, middlewares for logging and auth
	//handler.Use(cors.New(cors.DefaultConfig()))

	// Use our validation middleware to check all requests against the
	// OpenAPI schema.
	handler.Use(
		limits.RequestSizeLimiter(maxUploadLimit),
		middleware.OapiRequestValidatorWithOptions(swagger, &middleware.Options{}),
	)

	handler.MaxMultipartMemory = maxMultipartMemory

	api.RegisterHandlersWithOptions(handler, apiStore, api.GinServerOptions{})

	s := &http.Server{
		Handler:           handler,
		Addr:              fmt.Sprintf("0.0.0.0:%d", apiPort),
		ReadHeaderTimeout: maxReadHeaderTimeout,
		ReadTimeout:       maxReadTimeout,
		WriteTimeout:      maxWriteTimeout,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	return s
}
