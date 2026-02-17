package proxy

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/pool"
	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/template"
)

func handler(p *pool.ProxyPool, getDestination func(r *http.Request) (*pool.Destination, error), connLimitConfig *ConnectionLimitConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		d, err := getDestination(r)

		var mhe MissingHeaderError
		if errors.As(err, &mhe) {
			logger.L().Warn(ctx, "missing header", zap.Error(mhe))
			http.Error(w, "missing header", http.StatusBadRequest)

			return
		}

		if errors.Is(err, ErrInvalidHost) {
			logger.L().Warn(ctx, "invalid host", zap.String("host", r.Host))
			http.Error(w, "Invalid host", http.StatusBadRequest)

			return
		}

		var invalidPortErr *InvalidSandboxPortError
		if errors.As(err, &invalidPortErr) {
			logger.L().Warn(ctx, "invalid sandbox port", zap.String("host", r.Host), zap.String("port", invalidPortErr.Port))
			http.Error(w, "Invalid sandbox port", http.StatusBadRequest)

			return
		}

		var notFoundErr *SandboxNotFoundError
		if errors.As(err, &notFoundErr) {
			logger.L().Warn(ctx, "sandbox not found",
				zap.String("host", r.Host),
				logger.WithSandboxID(notFoundErr.SandboxId))

			err := template.
				NewSandboxNotFoundError(notFoundErr.SandboxId, r.Host).
				HandleError(w, r)
			if err != nil {
				logger.L().Error(ctx, "failed to handle sandbox not found error", zap.Error(err), logger.WithSandboxID(notFoundErr.SandboxId))
				http.Error(w, "Failed to handle sandbox not found error", http.StatusInternalServerError)

				return
			}

			return
		}

		var trafficMissingTokenErr *MissingTrafficAccessTokenError
		if errors.As(err, &trafficMissingTokenErr) {
			logger.L().Warn(ctx, "traffic access token is missing", zap.String("host", r.Host))

			err = template.
				NewTrafficAccessTokenMissingHeader(trafficMissingTokenErr.SandboxId, r.Host, trafficMissingTokenErr.Header).
				HandleError(w, r)
			if err != nil {
				logger.L().Error(ctx, "failed to handle traffic missing traffic access token header error", zap.Error(err), logger.WithSandboxID(trafficMissingTokenErr.SandboxId))
				http.Error(w, "Failed to handle invalid missing access token header error", http.StatusInternalServerError)

				return
			}

			return
		}

		var trafficInvalidTokenErr *InvalidTrafficAccessTokenError
		if errors.As(err, &trafficInvalidTokenErr) {
			logger.L().Warn(ctx, "traffic access token is invalid", zap.String("host", r.Host))

			err = template.
				NewTrafficAccessTokenInvalidHeader(trafficInvalidTokenErr.SandboxId, r.Host, trafficInvalidTokenErr.Header).
				HandleError(w, r)
			if err != nil {
				logger.L().Error(ctx, "failed to handle traffic invalid traffic access token header error", zap.Error(err), logger.WithSandboxID(trafficInvalidTokenErr.SandboxId))
				http.Error(w, "Failed to handle invalid traffic access token header error", http.StatusInternalServerError)

				return
			}

			return
		}

		if err != nil {
			logger.L().Error(ctx, "failed to route request", zap.Error(err), zap.String("host", r.Host))
			http.Error(w, fmt.Sprintf("Unexpected error when routing request: %s", err), http.StatusInternalServerError)

			return
		}

		// Connection limiting
		if connLimitConfig != nil {
			maxLimit := connLimitConfig.GetMaxLimit(ctx)
			count, acquired := connLimitConfig.Limiter.TryAcquire(d.SandboxId, maxLimit)
			if !acquired {
				logger.L().Warn(ctx, "sandbox too many incoming connections",
					zap.String("host", r.Host),
					logger.WithSandboxID(d.SandboxId),
					zap.Int("connection_limit", maxLimit))

				if connLimitConfig.OnConnectionBlocked != nil {
					connLimitConfig.OnConnectionBlocked(ctx)
				}

				err := template.
					NewSandboxTooManyConnectionsError(d.SandboxId, r.Host, maxLimit).
					HandleError(w, r)
				if err != nil {
					logger.L().Error(ctx, "failed to handle too many connections error", zap.Error(err), logger.WithSandboxID(d.SandboxId))
					http.Error(w, "Failed to handle too many connections error", http.StatusInternalServerError)

					return
				}

				return
			}

			start := time.Now()
			defer func() {
				connLimitConfig.Limiter.Release(d.SandboxId)
				if connLimitConfig.OnConnectionReleased != nil {
					connLimitConfig.OnConnectionReleased(ctx, time.Since(start).Milliseconds())
				}
			}()

			if connLimitConfig.OnConnectionAcquired != nil {
				connLimitConfig.OnConnectionAcquired(ctx, count)
			}
		}

		d.RequestLogger.Debug(ctx, "proxying request")

		ctx = pool.WithDestination(ctx, d)
		r = r.WithContext(ctx)

		proxy := p.Get(ctx, d)
		proxy.ServeHTTP(w, r)
	}
}
