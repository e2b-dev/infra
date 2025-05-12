package pool

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/tracking"
)

type proxyClient struct {
	httputil.ReverseProxy
	transport *http.Transport
}

func newProxyClient(
	maxIdleConns,
	maxHostIdleConns int,
	idleTimeout time.Duration,
	totalConnsCounter *atomic.Uint64,
	currentConnsCounter *atomic.Int64,
	logger *log.Logger,
) *proxyClient {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		// Limit the max connection per host to avoid exhausting the number of available ports to one host.
		MaxIdleConnsPerHost:   maxHostIdleConns,
		MaxIdleConns:          maxIdleConns,
		IdleConnTimeout:       idleTimeout,
		TLSHandshakeTimeout:   0,
		ResponseHeaderTimeout: 0,
		// TCP configuration
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := (&net.Dialer{
				Timeout:   30 * time.Second, // Connect timeout (no timeout by default)
				KeepAlive: 20 * time.Second, // Lower than our http keepalives (50 seconds)
			}).DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}

			totalConnsCounter.Add(1)

			return tracking.NewConnection(conn, currentConnsCounter), nil
		},
		DisableCompression: true, // No need to request or manipulate compression
	}

	return &proxyClient{
		transport: transport,
		ReverseProxy: httputil.ReverseProxy{
			Transport: transport,
			Rewrite: func(r *httputil.ProxyRequest) {
				t, ok := r.In.Context().Value(DestinationContextKey{}).(*Destination)
				if !ok {
					zap.L().Error("failed to get routing destination from context")

					// Error from this will be later caught as r.Host == "" in the ErrorHandler
					r.SetURL(r.In.URL)

					return
				}

				r.SetURL(t.Url)
				// We are **not** using SetXForwarded() because servers can sometimes modify the content-location header to be http which might break some customer services.
				r.Out.Host = r.In.Host
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				if r.Host == "" {
					zap.L().Error("error handler called from rewrite because of missing DestinationContext", zap.Error(err))

					http.Error(w, "Failed to route request to sandbox", http.StatusInternalServerError)

					return
				}

				t, ok := r.Context().Value(DestinationContextKey{}).(*Destination)
				if !ok {
					zap.L().Error("failed to get routing destination from context")

					http.Error(w, "Failed to route request to sandbox", http.StatusInternalServerError)

					return
				}

				if t.DefaultToPortError {
					err = template.
						NewPortClosedError(t.SandboxId, r.Host, t.SandboxPort).
						HandleError(w, r)
					if err != nil {
						zap.L().Error("failed to handle error", zap.Error(err))

						http.Error(w, "Failed to handle closed port error", http.StatusInternalServerError)

						return
					}
				}

				zap.L().Warn("sandbox error handler called", zap.Error(err))

				return
			},
			ModifyResponse: func(r *http.Response) error {
				t, ok := r.Request.Context().Value(DestinationContextKey{}).(*Destination)
				if !ok {
					zap.L().Error("failed to get routing target from context")

					return nil
				}

				if r.StatusCode >= 500 {
					t.RequestLogger.Error(
						"Reverse proxy error",
						zap.Int("status_code", r.StatusCode),
					)
				} else {
					t.RequestLogger.Debug("Reverse proxy response",
						zap.Int("status_code", r.StatusCode),
					)
				}

				return nil
			},
			// Ideally we would add info about sandbox to each error log, but there is no easy way right now.
			ErrorLog: logger,
		},
	}
}

func (p *proxyClient) closeIdleConnections() {
	p.transport.CloseIdleConnections()
}
