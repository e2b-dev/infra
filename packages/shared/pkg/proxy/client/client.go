package client

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/template"
)

type ProxyingInfoContextKey struct{}

// ProxingInfo contains information about where to route the request.
type ProxingInfo struct {
	Url       *url.URL
	SandboxId string
	Logger    *zap.Logger
	// ConnectionKey is used for identifying which keepalive connections are not the same so we can prevent unintended reuse.
	// This is evaluated before checking for existing connection to the IP:port pair.
	ConnectionKey string
}

type ProxyClient struct {
	httputil.ReverseProxy
	transport *http.Transport
}

func NewProxyClient(
	maxIdleConns,
	maxHostIdleConns int,
	idleTimeout time.Duration,
	totalConnsCounter *atomic.Uint64,
	currentConnsCounter *atomic.Int64,
	logger *log.Logger,
) *ProxyClient {
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

			return newTrackedConnection(conn, currentConnsCounter), nil
		},
		DisableCompression: true, // No need to request or manipulate compression
	}

	return &ProxyClient{
		transport: transport,
		ReverseProxy: httputil.ReverseProxy{
			Transport: transport,
			Rewrite: func(r *httputil.ProxyRequest) {
				t, ok := r.In.Context().Value(ProxyingInfoContextKey{}).(*ProxingInfo)
				if !ok {
					zap.L().Error("failed to get routing target from context")

					return
				}

				r.SetURL(t.Url)

				r.Out.Host = r.In.Host
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				t, ok := r.Context().Value(ProxyingInfoContextKey{}).(*ProxingInfo)
				if !ok {
					zap.L().Error("failed to get routing target from context")

					return
				}

				err = template.
					NewPortClosedError(t.SandboxId, r.Host, t.Url.Port()).
					HandleError(w, r)
				if err != nil {
					zap.L().Error("failed to handle error", zap.Error(err))
					http.Error(w, "Failed to handle closed port error", http.StatusInternalServerError)
				}
			},
			ModifyResponse: func(r *http.Response) error {
				t, ok := r.Request.Context().Value(ProxyingInfoContextKey{}).(*ProxingInfo)
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
			ErrorLog: logger,
		},
	}
}

func (p *ProxyClient) CloseIdleConnections() {
	p.transport.CloseIdleConnections()
}
