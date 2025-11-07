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
	activeConnections *smap.Map[*tracking.Connection]
	destination       *Destination
	transport         *http.Transport
}

func (p *ProxyClient) WithDestination(destination *Destination) *ProxyClient {
	return &ProxyClient{
		activeConnections: p.activeConnections,
		destination:       destination,
		transport:         p.transport,
	}
}

func newReverseProxy(pc *ProxyClient) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Transport: pc.transport,
		Rewrite: func(r *httputil.ProxyRequest) {
			t, ok := pc.getDestination(r.In)
			if !ok {
				// Error from this will be later caught as r.Host == "" in the ErrorHandler
				r.SetURL(r.In.URL)

				return
			}

			r.SetURL(t.Url)
			// We are **not** using SetXForwarded() because servers can sometimes modify the content-location header to be http which might break some customer services.
			r.Out.Host = r.In.Host
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			t, ok := pc.getDestination(r)
			if !ok {
				http.Error(w, "Failed to route request to sandbox", http.StatusInternalServerError)

				return
			}

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
			t, ok := pc.getDestination(r.Request)
			if !ok {
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
		ErrorLog: log.New(os.Stdout, "[proxy] ", log.LstdFlags),
	}
}

func newProxyClient(
	maxIdleConns,
	maxHostIdleConns int,
	maxConnectionAttempts int,
	idleTimeout time.Duration,
	totalConnsCounter *atomic.Uint64,
	currentConnsCounter *atomic.Int64,
) *ProxyClient {
	activeConnections := smap.New[*tracking.Connection]()
	pc := &ProxyClient{
		activeConnections: activeConnections,
		transport: &http.Transport{
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
		},
	}

	return pc
}

func (p *ProxyClient) getDestination(r *http.Request) (*Destination, bool) {
	if p.destination == nil {
		zap.L().Error("failed to get routing target from context",
			zap.String("request_method", r.Method),
			zap.String("request_url", r.URL.String()))

		return nil, false
	}

	return p.destination, true
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

func (p *ProxyClient) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	reverseProxy := newReverseProxy(p)
	reverseProxy.ServeHTTP(w, r)
}
