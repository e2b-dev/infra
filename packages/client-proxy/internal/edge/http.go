package edge

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	limits "github.com/gin-contrib/size"
	"github.com/gin-gonic/gin"
	middleware "github.com/oapi-codegen/gin-middleware"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/proxy/internal/edge/handlers"
	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
)

const (
	maxMultipartMemory = 1 << 27 // 128 MiB
	maxUploadLimit     = 1 << 28 // 256 MiB

	maxReadHeaderTimeout = 60 * time.Second
	maxReadTimeout       = 75 * time.Second
	maxWriteTimeout      = 75 * time.Second
)

func NewGinServer(ctx context.Context, logger *zap.Logger, store *handlers.APIStore, port int, swagger *openapi3.T) *http.Server {
	// Clear out the servers array in the swagger spec, that skips validating
	// that server names match. We don't know how this thing will be run.
	swagger.Servers = nil

	gin.SetMode(gin.ReleaseMode)

	handler := gin.New()

	// todo: setup CORS, middlewares for logging and auth
	//handler.Use(cors.New(cors.DefaultConfig()))

	// Use our validation middleware to check all requests against the
	// OpenAPI schema.
	handler.Use(
		gin.Recovery(),
		limits.RequestSizeLimiter(maxUploadLimit),
		middleware.OapiRequestValidatorWithOptions(
			swagger,
			&middleware.Options{
				//ErrorHandler:      utils.ErrorHandler,
				//MultiErrorHandler: utils.MultiErrorHandler,
				Options: openapi3filter.Options{
					MultiError: true,
					AuthenticationFunc: func(ctx context.Context, input *openapi3filter.AuthenticationInput) error {
						// todo: implement authentication
						return nil
					},
				},
			},
		),
	)

	handler.MaxMultipartMemory = maxMultipartMemory

	api.RegisterHandlersWithOptions(handler, store, api.GinServerOptions{})

	s := &http.Server{
		Handler:           handler,
		Addr:              fmt.Sprintf("0.0.0.0:%d", port),
		ReadHeaderTimeout: maxReadHeaderTimeout,
		ReadTimeout:       maxReadTimeout,
		WriteTimeout:      maxWriteTimeout,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	return s
}
