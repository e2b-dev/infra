package pool

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/tracking"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type ProxyClient struct {
	httputil.ReverseProxy

	transport *http.Transport

	activeConnections *smap.Map[*tracking.Connection]
}

func newProxyClient(
	t *Destination,
	maxIdleConns,
	maxHostIdleConns int,
	maxConnectionAttempts int,
	idleTimeout time.Duration,
	totalConnsCounter *atomic.Uint64,
	currentConnsCounter *atomic.Int64,
) *ProxyClient {
	activeConnections := smap.New[*tracking.Connection]()

	stdLogger, err := zap.NewStdLogAt(t.RequestLogger, zap.WarnLevel)
	if err != nil {
		t.RequestLogger.Warn("failed to create logger, falling back to stderr", zap.Error(err))
		stdLogger = log.New(os.Stderr, "", 0)
	}

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
			var conn net.Conn
			var err error

			// Retry connection attempts to handle port forwarding delays in sandbox envd.
			// When a process binds to localhost inside the sandbox, it can take up to 1s (delay is 1s + socat startup delay)
			// for the port scanner to detect it and start socat forwarding to the host IP.
			maxAttempts := max(maxConnectionAttempts, 1)
			for attempt := range maxAttempts {
				conn, err = (&net.Dialer{
					Timeout:   30 * time.Second,
					KeepAlive: 20 * time.Second,
				}).DialContext(ctx, network, addr)

				if err == nil {
					totalConnsCounter.Add(1)

					return tracking.NewConnection(conn, currentConnsCounter, activeConnections), nil
				}

				if ctx.Err() != nil {
					return nil, ctx.Err()
				}

				// Don't sleep on the last attempt
				if attempt < maxAttempts-1 {
					// Linear backoff: 100ms, 200ms, 300ms, 400ms
					backoff := time.Duration(100*(attempt+1)) * time.Millisecond
					select {
					case <-time.After(backoff):
						// Continue to next attempt
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
			}

			return nil, err
		},
		DisableCompression: true, // No need to request or manipulate compression
	}

	proxy := httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(t.Url)
			// We are **not** using SetXForwarded() because servers can sometimes modify the content-location header to be http which might break some customer services.
			r.Out.Host = r.In.Host
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if err != nil {
				t.RequestLogger.Error("sandbox error handler called", zap.Error(err))
			}

			if t.DefaultToPortError {
				err = template.
					NewPortClosedError(t.SandboxId, r.Host, t.SandboxPort).
					HandleError(w, r)
				if err != nil {
					t.RequestLogger.Error("failed to handle error", zap.Error(err))

					http.Error(w, "Failed to handle closed port error", http.StatusInternalServerError)

					return
				}

				return
			}

			http.Error(w, "Failed to route request to sandbox", http.StatusBadGateway)
		},
		ModifyResponse: func(r *http.Response) error {
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
		ErrorLog: stdLogger,
	}

	return &ProxyClient{
		transport:         transport,
		activeConnections: activeConnections,
		ReverseProxy:      proxy,
	}
}

func (p *ProxyClient) closeIdleConnections() {
	p.transport.CloseIdleConnections()
}

func (p *ProxyClient) resetAllConnections() error {
	var errs []error

	for _, conn := range p.activeConnections.Items() {
		err := conn.Reset()
		if err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}
