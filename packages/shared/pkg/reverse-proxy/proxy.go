package reverse_proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"regexp"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/meters"
	"github.com/e2b-dev/infra/packages/shared/pkg/reverse-proxy/host"
	template "github.com/e2b-dev/infra/packages/shared/pkg/reverse-proxy/template-errors"
)

const (
	maxConnectionDuration = 24 * time.Hour // The same as the current max sandbox duration.
	maxIdleConnections    = 32768          // Reasonably big number that is lower than the number of available ports.
)

func New(
	port uint,
	idleTimeout time.Duration,
	connectionTimeout time.Duration,
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
		Handler:           http.HandlerFunc(proxyHandler(transport, getTargetHost)),
	}
}

func proxyHandler(
	transport *http.Transport,
	getSandboxHost func(r *http.Request) (*host.SandboxHost, error),
) func(w http.ResponseWriter, r *http.Request) {
	var activeConnections *metric.Int64UpDownCounter

	connectionCounter, err := meters.GetUpDownCounter(meters.OrchestratorProxyActiveConnectionsCounterMeterName)
	if err != nil {
		zap.L().Error("failed to create active connections counter", zap.Error(err))
	} else {
		activeConnections = &connectionCounter
	}

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

var browserRegex = regexp.MustCompile(`(?i)mozilla|chrome|safari|firefox|edge|opera|msie`)

func isBrowser(r *http.Request) bool {
	return browserRegex.MatchString(r.UserAgent())
}

func handleError[T any](
	w http.ResponseWriter,
	r *http.Request,
	err *template.ReturnedError[T],
	logger *zap.Logger,
) {
	if isBrowser(r) {
		body, buildErr := err.BuildHtml()
		if buildErr != nil {
			logger.Error("Failed to build HTML error response", zap.Error(buildErr))
			w.WriteHeader(http.StatusInternalServerError)

			return
		}

		w.WriteHeader(http.StatusBadGateway)
		w.Header().Add("Content-Type", "text/html")
		_, writeErr := w.Write(body)
		if writeErr != nil {
			logger.Error("failed to write HTML error response", zap.Error(writeErr))
		}

		return
	}

	body, buildErr := err.BuildJson()
	if buildErr != nil {
		logger.Error("failed to build JSON error response", zap.Error(buildErr))

		return
	}

	w.WriteHeader(http.StatusBadGateway)
	w.Header().Add("Content-Type", "application/json")

	_, writeErr := w.Write(body)
	if writeErr != nil {
		logger.Error("failed to write JSON error response", zap.Error(writeErr))
	}
}
