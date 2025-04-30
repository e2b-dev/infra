package client

import (
	"context"
	"net"
	"net/http"
	"net/http/httputil"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	template "github.com/e2b-dev/infra/packages/shared/pkg/reverse-proxy/error-template"
)

type ProxyClient struct {
	httputil.ReverseProxy
	totalConnections *atomic.Uint64
}

func NewProxyClient(idleTimeout time.Duration, maxIdleConnections, maxIdleConnectionsPerHost int) (*ProxyClient, error) {
	var totalConnections atomic.Uint64

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		// Limit the max connection per host to avoid exhausting the number of available ports to one host.
		MaxIdleConnsPerHost:   maxIdleConnectionsPerHost,
		MaxIdleConns:          maxIdleConnections,
		IdleConnTimeout:       idleTimeout,
		TLSHandshakeTimeout:   0,
		ResponseHeaderTimeout: 0,
		// TCP configuration
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := (&net.Dialer{
				Timeout:   30 * time.Second, // Connect timeout (no timeout by default)
				KeepAlive: 20 * time.Second, // Lower than our http keepalives (50 seconds)
			}).DialContext(ctx, network, addr)
			if err == nil {
				totalConnections.Add(1)

				return conn, nil
			}

			return nil, err
		},
		DisableCompression: true, // No need to request or manipulate compression
	}

	proxyLogger, err := zap.NewStdLogAt(zap.L(), zap.ErrorLevel)
	if err != nil {
		return nil, err
	}

	return &ProxyClient{
		ReverseProxy: httputil.ReverseProxy{
			Transport: transport,
			Rewrite: func(r *httputil.ProxyRequest) {
				t, ok := r.In.Context().Value(RoutingTargetContextKey{}).(*RoutingTarget)
				if !ok {
					zap.L().Error("failed to get routing target from context")

					return
				}

				r.SetURL(t.Url)

				r.Out.Host = r.In.Host
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				t, ok := r.Context().Value(RoutingTargetContextKey{}).(*RoutingTarget)
				if !ok {
					zap.L().Error("failed to get routing target from context")

					return
				}

				errorTemplate := template.NewPortClosedError(t.SandboxId, r.Host, t.Url.Port())

				err = template.HandleError(w, r, errorTemplate, t.Logger)
				if err != nil {
					zap.L().Error("failed to handle error", zap.Error(err))
					http.Error(w, "Failed to handle error", http.StatusInternalServerError)
				}
			},
			ModifyResponse: func(r *http.Response) error {
				t, ok := r.Request.Context().Value(RoutingTargetContextKey{}).(*RoutingTarget)
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
		},
		totalConnections: &totalConnections,
	}, nil
}

func (p *ProxyClient) TotalConnections() uint64 {
	return p.totalConnections.Load()
}

// TODO: Add the current connection via wrapping DialContext.
