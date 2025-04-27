package reverse_proxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	template "github.com/e2b-dev/infra/packages/shared/pkg/reverse-proxy/error-template"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const (
	maxConnectionDuration = 24 * time.Hour // The same as the current max sandbox duration.
	maxIdleConnections    = 32768          // Reasonably big number that is lower than the number of available ports.
)

type routingTargetContextKey struct{}

// RoutingTarget contains information about where to route the request.
type RoutingTarget struct {
	Url       *url.URL
	SandboxId string
	Logger    *zap.Logger
}

var (
	proxies = smap.New[*httputil.ReverseProxy]()
)

func New(
	port uint,
	idleTimeout time.Duration,
	connectionTimeout time.Duration,
	activeConnections *metric.Int64UpDownCounter,
	getRoutingTarget func(r *http.Request) (*RoutingTarget, error),
) *http.Server {
	return &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		ReadTimeout:       maxConnectionDuration,
		WriteTimeout:      maxConnectionDuration,
		IdleTimeout:       idleTimeout,
		ReadHeaderTimeout: 20 * time.Second,
		Handler: http.HandlerFunc(
			proxyHandler(
				getRoutingTarget,
				activeConnections,
				idleTimeout,
				connectionTimeout,
			),
		),
	}
}

func proxyHandler(
	getRoutingTarget func(r *http.Request) (*RoutingTarget, error),
	activeConnections *metric.Int64UpDownCounter,
	idleTimeout time.Duration,
	connectionTimeout time.Duration,
) func(w http.ResponseWriter, r *http.Request) {
	proxyLogger, _ := zap.NewStdLogAt(zap.L(), zap.ErrorLevel)

	getProxy := func(target *RoutingTarget) *httputil.ReverseProxy {
		proxy, ok := proxies.Get(target.SandboxId)
		if ok {
			return proxy
		}

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

		proxy = &httputil.ReverseProxy{
			Transport: transport,
			Rewrite: func(r *httputil.ProxyRequest) {
				t, ok := r.In.Context().Value(routingTargetContextKey{}).(*RoutingTarget)
				if !ok {
					zap.L().Error("failed to get routing target from context")

					return
				}

				r.SetURL(t.Url)

				r.Out.Host = r.In.Host
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				t, ok := r.Context().Value(routingTargetContextKey{}).(*RoutingTarget)
				if !ok {
					zap.L().Error("failed to get routing target from context")

					return
				}

				rc := http.NewResponseController(w)

				err = rc.EnableFullDuplex()
				if err != nil {
					zap.L().Error("failed to enable full duplex", zap.Error(err))
				}

				t.Logger.Error("Reverse proxy error", zap.Error(err))

				errorTemplate := template.NewPortClosedError(t.SandboxId, r.Host, t.Url.Port())
				handleError(w, r, errorTemplate, t.Logger)
			},
			ModifyResponse: func(r *http.Response) error {
				t, ok := r.Request.Context().Value(routingTargetContextKey{}).(*RoutingTarget)
				if !ok {
					zap.L().Error("failed to get routing target from context")

					return nil
				}

				if r.StatusCode >= 500 {
					t.Logger.Error(
						"Reverse proxy error",
						zap.Int("status_code", r.StatusCode),
					)
				} else {
					t.Logger.Debug("Reverse proxy response",
						zap.Int("status_code", r.StatusCode),
					)
				}

				return nil
			},
			// Ideally we would add info about sandbox to each error log, but there is no easy way right now.
			ErrorLog: proxyLogger,
		}

		proxies.Insert(target.SandboxId, proxy)

		return proxy
	}

	return func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)

		err := rc.EnableFullDuplex()
		if err != nil {
			zap.L().Error("failed to enable full duplex", zap.Error(err))
		}

		if activeConnections != nil {
			// TODO: Won't cancellation of the request make adding/removing from the counter unpredictable?
			// We should probably use observable gauge and separate counter without context.
			// Also not 100% sure if this handled idle connections/streaming correctly.
			(*activeConnections).Add(r.Context(), 1)
			defer func() {
				(*activeConnections).Add(r.Context(), -1)
			}()
		}

		t, err := getRoutingTarget(r)
		if errors.As(err, &ErrInvalidHost{}) {
			zap.L().Warn("invalid host", zap.String("host", r.Host))
			http.Error(w, "Invalid host", http.StatusBadRequest)

			return
		}

		if errors.As(err, &ErrInvalidSandboxPort{}) {
			t.Logger.Warn("invalid sandbox port")
			http.Error(w, "Invalid sandbox port", http.StatusBadRequest)

			return
		}

		if errors.As(err, &ErrSandboxNotFound{}) {
			t.Logger.Warn("sandbox not found")

			errorTemplate := template.NewSandboxNotFoundError(t.SandboxId, r.Host)
			handleError(w, r, errorTemplate, t.Logger)

			return
		}

		if err != nil {
			t.Logger.Error("failed to route request", zap.Error(err))
			http.Error(w, "Unexpected error when routing request", http.StatusInternalServerError)

			return
		}

		t.Logger.Debug("proxying request")

		ctx := context.WithValue(r.Context(), routingTargetContextKey{}, t)

		getProxy(t).ServeHTTP(w, r.WithContext(ctx))
	}
}
