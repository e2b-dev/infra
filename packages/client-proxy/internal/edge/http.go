package edge

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	limits "github.com/gin-contrib/size"
	ginzap "github.com/gin-contrib/zap"
	"github.com/gin-gonic/gin"
	middleware "github.com/oapi-codegen/gin-middleware"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/proxy/internal/edge/handlers"
	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	securityHeaderName = "X-API-Key"
	securitySchemaName = "ApiKeyAuth"

	forbiddenErrPrefix = "access forbidden: "
	securityErrPrefix  = "error in openapi3filter.SecurityRequirementsError: security requirements failed: "

	maxMultipartMemory = 1 << 27 // 128 MiB
	maxUploadLimit     = 1 << 28 // 256 MiB

	maxReadHeaderTimeout = 60 * time.Second
	maxReadTimeout       = 75 * time.Second
	maxWriteTimeout      = 75 * time.Second
)

var (
	ErrMissingAuthHeader = errors.New("authorization header is missing")
	ErrInvalidAuth       = errors.New("authorization is invalid")
)

func NewGinServer(ctx context.Context, logger *zap.Logger, store *handlers.APIStore, swagger *openapi3.T, tracer trace.Tracer, port int, authSecret string) *http.Server {
	// Clear out the servers array in the swagger spec, that skips validating
	// that server names match. We don't know how this thing will be run.
	swagger.Servers = nil

	gin.SetMode(gin.ReleaseMode)

	handler := gin.New()
	handler.MaxMultipartMemory = maxMultipartMemory

	// Use our validation middleware to check all requests against the
	// OpenAPI schema.
	handler.Use(
		gin.Recovery(),
		limits.RequestSizeLimiter(maxUploadLimit),
		middleware.OapiRequestValidatorWithOptions(
			swagger,
			&middleware.Options{
				ErrorHandler: ginErrorHandler,
				Options: openapi3filter.Options{
					AuthenticationFunc: ginBuildAuthenticationHandler(tracer, authSecret),
					MultiError:         false,
				},
			},
		),

		func(c *gin.Context) {
			path := c.Request.URL.Path
			pathSkipped := []string{"/health", "/health/traffic"}

			if slices.Contains(pathSkipped, path) {
				c.Next()
				return
			}

			func(c *gin.Context) {
				reqLogger := logger
				ginzap.Ginzap(reqLogger, time.RFC3339Nano, true)(c)
			}(c)
		},
	)

	api.RegisterHandlersWithOptions(handler, store, api.GinServerOptions{})

	s := &http.Server{
		Handler:           handler,
		Addr:              fmt.Sprintf("0.0.0.0:%d", port),
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ReadHeaderTimeout: maxReadHeaderTimeout,
		ReadTimeout:       maxReadTimeout,
		WriteTimeout:      maxWriteTimeout,
	}

	return s
}

func ginBuildAuthenticationHandler(tracer trace.Tracer, authSecret string) func(ctx context.Context, input *openapi3filter.AuthenticationInput) error {
	return func(ctx context.Context, input *openapi3filter.AuthenticationInput) error {
		ginContext := ctx.Value(middleware.GinContextKey).(*gin.Context)
		requestContext := ginContext.Request.Context()

		_, span := tracer.Start(requestContext, "authenticate")
		defer span.End()

		if input.SecuritySchemeName != securitySchemaName {
			return fmt.Errorf("invalid security scheme name '%s'", input.SecuritySchemeName)
		}

		request := input.RequestValidationInput.Request
		key := request.Header.Get(securityHeaderName)

		// Check for the Authorization header.
		if key == "" {
			return ErrMissingAuthHeader
		}

		// Check if the key matches the secret.
		if key != authSecret {
			return ErrInvalidAuth
		}

		return nil
	}
}

func ginErrorHandler(c *gin.Context, message string, statusCode int) {
	var errMsg error

	ctx := c.Request.Context()

	data, err := c.GetRawData()
	if err == nil {
		errMsg = fmt.Errorf("OpenAPI validation error: %s, data: %s", message, data)
	} else {
		errMsg = fmt.Errorf("OpenAPI validation error: %s, body read error: %w", message, err)
	}

	telemetry.ReportError(ctx, message, errMsg)

	c.Error(errMsg)

	// Handle forbidden errors
	if strings.HasPrefix(message, forbiddenErrPrefix) {
		c.AbortWithStatusJSON(
			http.StatusForbidden,
			gin.H{
				"code":    http.StatusForbidden,
				"message": strings.TrimPrefix(message, forbiddenErrPrefix),
			},
		)

		return
	}

	// Handle security requirements errors from the openapi3filter
	if strings.HasPrefix(message, securityErrPrefix) {
		c.AbortWithStatusJSON(
			http.StatusUnauthorized,
			gin.H{
				"code":    http.StatusUnauthorized,
				"message": strings.TrimPrefix(message, securityErrPrefix),
			},
		)

		return
	}

	c.AbortWithStatusJSON(statusCode, gin.H{"code": statusCode, "message": fmt.Errorf("validation error: %s", message).Error()})
}
