package pool

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
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
	maxIdleConns,
	maxHostIdleConns int,
	maxConnectionAttempts int,
	idleTimeout time.Duration,
	totalConnsCounter *atomic.Uint64,
	currentConnsCounter *atomic.Int64,
	l *log.Logger,
	disableKeepAlives bool,
) *ProxyClient {
	activeConnections := smap.New[*tracking.Connection]()

	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		// Limit the max connection per host to avoid exhausting the number of available ports to one host.
		MaxIdleConnsPerHost:   maxHostIdleConns,
		MaxIdleConns:          maxIdleConns,
		IdleConnTimeout:       idleTimeout,
		TLSHandshakeTimeout:   0,
		ResponseHeaderTimeout: 0,
		DisableKeepAlives:     disableKeepAlives,
		ForceAttemptHTTP2:     false,
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

	pc := &ProxyClient{
		transport:         transport,
		activeConnections: activeConnections,
	}

	pc.ReverseProxy = httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(r *httputil.ProxyRequest) {
			t, ok := pc.getDestination(r.In)
			if !ok {
				r.SetURL(r.In.URL) // make linters happy, shouldn't matter

				return
			}

			r.SetURL(t.Url)

			if t.MaskRequestHost != nil {
				// Mask the request host to bypass source host protections.
				r.Out.Header.Set("X-Forwarded-Host", r.In.Host)
				r.Out.Host = *t.MaskRequestHost
			} else {
				// We are **not** using SetXForwarded() because servers can sometimes modify the content-location header to be http which might break some customer services.
				r.Out.Host = r.In.Host
			}
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			ctx := r.Context()
			t, ok := pc.getDestination(r)
			if !ok {
				logger.L().Error(ctx, "proxy request without sandbox received error", zap.Error(err))
				http.Error(w, "Failed to route request to sandbox", http.StatusInternalServerError)

				return
			}

			if r.Host == "" { // kept around for historical reasons, unsure of usefulness. todo: find out if this is useful.
				t.RequestLogger.Error(ctx, "error handler called from rewrite because of missing DestinationContext", zap.Error(err))
				http.Error(w, "Failed to route request to sandbox", http.StatusInternalServerError)

				return
			}

			if err != nil {
				t.RequestLogger.Error(ctx, "sandbox error handler called", zap.Error(err))
			}

			if t.DefaultToPortError {
				err = template.
					NewPortClosedError(t.SandboxId, r.Host, t.SandboxPort).
					HandleError(w, r)
				if err != nil {
					logger.L().Error(ctx, "failed to handle error", zap.Error(err))

					http.Error(w, "Failed to handle closed port error", http.StatusInternalServerError)

					return
				}

				return
			}

			http.Error(w, "Failed to route request to sandbox", http.StatusBadGateway)
		},
		ModifyResponse: func(r *http.Response) error {
			ctx := r.Request.Context()
			t, ok := pc.getDestination(r.Request)
			if !ok {
				return nil
			}

			if r.StatusCode >= 500 {
				t.RequestLogger.Warn(
					ctx,
					"Reverse proxy error",
					zap.Int("status_code", r.StatusCode),
				)
			} else {
				t.RequestLogger.Debug(ctx, "Reverse proxy response",
					zap.Int("status_code", r.StatusCode),
				)
			}

			return nil
		},
		// Ideally we would add info about sandbox to each error log, but there is no easy way right now.
		ErrorLog: l,
	}

	return pc
}

func (p *ProxyClient) getDestination(r *http.Request) (*Destination, bool) {
	ctx := r.Context()
	d, ok := getDestination(ctx)
	if !ok {
		logger.L().Error(ctx, "failed to get routing target from context",
			zap.String("request_method", r.Method),
			zap.String("request_url", r.URL.String()))

		return nil, false
	}

	return d, true
}

func (p *ProxyClient) closeIdleConnections() {
	p.transport.CloseIdleConnections()
}

func (p *ProxyClient) resetAllConnections() error {
	var errs []error

	for _, conn := range p.activeConnections.Items() {
		err := conn.Reset()
		if err != nil && !errors.Is(err, net.ErrClosed) {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}
