package reverse_proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	template "github.com/e2b-dev/infra/packages/shared/pkg/reverse-proxy/error-template"
	"github.com/e2b-dev/infra/packages/shared/pkg/reverse-proxy/host"
)

const (
	maxConnectionDuration = 24 * time.Hour // The same as the current max sandbox duration.
	maxIdleConnections    = 32768          // Reasonably big number that is lower than the number of available ports.
)

func New(
	port uint,
	idleTimeout time.Duration,
	connectionTimeout time.Duration,
	activeConnections *metric.Int64UpDownCounter,
	getTargetHost func(r *http.Request) (*host.SandboxHost, error),
) *http.Server {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConnsPerHost:   maxIdleConnections,
		IdleConnTimeout:       idleTimeout,
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		// TCP configuration
		DialContext: (&net.Dialer{
			Timeout:   connectionTimeout, // Connect timeout (no timeout by default)
			KeepAlive: 30 * time.Second,  // Lower than our http keepalives (50 seconds)
		}).DialContext,
		DisableCompression: true, // No need to request or manipulate compression
	}

	return &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		ReadTimeout:       maxConnectionDuration,
		WriteTimeout:      maxConnectionDuration,
		IdleTimeout:       idleTimeout,
		ReadHeaderTimeout: 20 * time.Second,
		Handler:           http.HandlerFunc(proxyHandler(transport, getTargetHost, activeConnections)),
	}
}

func proxyHandler(
	transport *http.Transport,
	getSandboxHost func(r *http.Request) (*host.SandboxHost, error),
	activeConnections *metric.Int64UpDownCounter,
) func(w http.ResponseWriter, r *http.Request) {
	proxyLogger, _ := zap.NewStdLogAt(zap.L(), zap.ErrorLevel)

	proxy := httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(r *httputil.ProxyRequest) {
			host, ok := r.In.Context().Value(host.SandboxHostContextKey{}).(*host.SandboxHost)
			if !ok {
				zap.L().Error("failed to get sandox host info from context")

				return
			}

			r.SetURL(host.Url)

			r.Out.Host = r.In.Host
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			h, ok := r.Context().Value(host.SandboxHostContextKey{}).(*host.SandboxHost)
			if !ok {
				zap.L().Error("failed to get sandox host info from context")

				return
			}

			h.Logger.Error("Reverse proxy error", zap.Error(err))

			errorTemplate := template.NewPortClosedError(h.SandboxId, r.Host, h.Url.Port())
			handleError(w, r, errorTemplate, h.Logger)
		},
		ModifyResponse: func(r *http.Response) error {
			h, ok := r.Request.Context().Value(host.SandboxHostContextKey{}).(*host.SandboxHost)
			if !ok {
				zap.L().Error("failed to get sandox host info from context")

				return nil
			}

			if r.StatusCode >= 500 {
				h.Logger.Error(
					"Reverse proxy error",
					zap.Int("status_code", r.StatusCode),
				)
			} else {
				h.Logger.Debug("Reverse proxy response",
					zap.Int("status_code", r.StatusCode),
				)
			}

			return nil
		},
		// Ideally we would add info about sandbox to each error log, but there is no easy way right now.
		ErrorLog: proxyLogger,
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if activeConnections != nil {
			// TODO: Won't cancellation of the request make adding/removing from the counter unpredictable?
			// We should probably use observable gauge and separate counter without context.
			// Also not 100% sure if this handled idle connections/streaming correctly.
			(*activeConnections).Add(r.Context(), 1)
			defer func() {
				(*activeConnections).Add(r.Context(), -1)
			}()
		}

		h, err := getSandboxHost(r)
		if errors.As(err, &host.ErrInvalidHost{}) {
			zap.L().Warn("invalid host to proxy", zap.String("host", r.Host))
			http.Error(w, "Invalid host", http.StatusBadRequest)

			return
		}

		if errors.As(err, &host.ErrInvalidSandboxPort{}) {
			h.Logger.Warn("invalid sandbox port", zap.String("sandbox_port", h.Url.Port()))
			http.Error(w, "Invalid sandbox port", http.StatusBadRequest)

			return
		}

		// TODO: Should this error be propagated differently?
		if errors.As(err, &host.ErrSandboxNotFound{}) {
			h.Logger.Warn("sandbox not found", zap.String("sandbox_id", h.SandboxId))

			errorTemplate := template.NewSandboxNotFoundError(h.SandboxId, r.Host)
			handleError(w, r, errorTemplate, h.Logger)

			return
		}

		if err != nil {
			h.Logger.Error("failed to get host", zap.Error(err))
			http.Error(w, "Internal server error", http.StatusInternalServerError)

			return
		}

		h.Logger.Debug("proxying request")

		ctx := context.WithValue(r.Context(), host.SandboxHostContextKey{}, h)

		proxy.ServeHTTP(w, r.WithContext(ctx))
	}
}
