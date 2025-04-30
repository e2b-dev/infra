package routing

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/reverse-proxy/client"
	template "github.com/e2b-dev/infra/packages/shared/pkg/reverse-proxy/error-template"
	"github.com/e2b-dev/infra/packages/shared/pkg/reverse-proxy/pool"
)

func Handle(pool *pool.ProxyPool, getRoutingTarget func(r *http.Request) (*client.RoutingTarget, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t, err := getRoutingTarget(r)

		var invalidHostErr *ErrInvalidHost
		if errors.As(err, &invalidHostErr) {
			zap.L().Warn("invalid host", zap.String("host", r.Host))
			http.Error(w, "Invalid host", http.StatusBadRequest)

			return
		}

		var invalidPortErr *ErrInvalidSandboxPort
		if errors.As(err, &invalidPortErr) {
			zap.L().Warn("invalid sandbox port", zap.String("host", r.Host))
			http.Error(w, "Invalid sandbox port", http.StatusBadRequest)

			return
		}

		var notFoundErr *ErrSandboxNotFound
		if errors.As(err, &notFoundErr) {
			zap.L().Warn("sandbox not found", zap.String("host", r.Host))

			errorTemplate := template.NewSandboxNotFoundError(notFoundErr.SandboxId, r.Host)
			template.HandleError(w, r, errorTemplate, zap.L())

			return
		}

		if err != nil {
			zap.L().Error("failed to route request", zap.Error(err), zap.String("host", r.Host))
			http.Error(w, fmt.Sprintf("Unexpected error when routing request: %s", err), http.StatusInternalServerError)

			return
		}

		t.Logger.Debug("proxying request")

		ctx := context.WithValue(r.Context(), client.RoutingTargetContextKey{}, t)

		proxy, err := pool.Get(t.ConnectionKey)
		if err != nil {
			zap.L().Error("failed to get proxy", zap.Error(err), zap.String("host", r.Host))
			http.Error(w, fmt.Sprintf("Unexpected error when routing request: %s", err), http.StatusInternalServerError)

			return
		}

		proxy.ServeHTTP(w, r.WithContext(ctx))
	}
}
