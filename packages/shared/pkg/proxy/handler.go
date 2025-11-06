package proxy

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/pool"
	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/template"
)

type InvalidHostError struct{}

func (e InvalidHostError) Error() string {
	return "invalid url host"
}

type SandboxNotFoundError struct {
	SandboxId string
}

func (e SandboxNotFoundError) Error() string {
	return "sandbox not found"
}

func handler(p *pool.ProxyPool, getDestination func(r *http.Request) (*pool.Destination, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d, err := getDestination(r)

		var invalidHostErr InvalidHostError
		if errors.As(err, &invalidHostErr) {
			zap.L().Warn("invalid host", zap.String("host", r.Host))
			http.Error(w, "Invalid host", http.StatusBadRequest)

			return
		}

		var notFoundErr SandboxNotFoundError
		if errors.As(err, &notFoundErr) {
			zap.L().Warn("sandbox not found",
				zap.String("host", r.Host),
				logger.WithSandboxID(notFoundErr.SandboxId))

			err := template.
				NewSandboxNotFoundError(notFoundErr.SandboxId, r.Host).
				HandleError(w, r)
			if err != nil {
				zap.L().Error("failed to handle sandbox not found error", zap.Error(err), logger.WithSandboxID(notFoundErr.SandboxId))
				http.Error(w, "Failed to handle sandbox not found error", http.StatusInternalServerError)

				return
			}

			return
		}

		if err != nil {
			zap.L().Error("failed to route request", zap.Error(err), zap.String("host", r.Host))
			http.Error(w, fmt.Sprintf("Unexpected error when routing request: %s", err), http.StatusInternalServerError)

			return
		}

		d.RequestLogger.Debug("proxying request")

		ctx := context.WithValue(r.Context(), pool.DestinationContextKey{}, d)

		proxy := p.Get(d)
		proxy.ServeHTTP(w, r.WithContext(ctx))
	}
}
