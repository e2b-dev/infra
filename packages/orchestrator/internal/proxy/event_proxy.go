package proxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// EventProxy handles outbound traffic from sandboxes calling the event.e2b.com domain
type EventProxy struct {
	server *http.Server
}

func NewEventProxy(port uint) *EventProxy {
	server := &http.Server{
		Addr: fmt.Sprintf(":%d", port),
	}

	return &EventProxy{
		server: server,
	}
}
func (p *EventProxy) Start() error {
	serverTransport := &http.Transport{
		MaxIdleConns:          1024,
		MaxIdleConnsPerHost:   8192,
		IdleConnTimeout:       620 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 24 * time.Hour,
		DisableKeepAlives:     true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			zap.L().Info("Dialing", zap.String("network", network), zap.String("addr", addr))
			return net.Dial(network, addr)
		},
	}
	p.server.Handler = http.HandlerFunc(p.proxyHandler(serverTransport))

	return p.server.ListenAndServeTLS("/etc/ssl/certs/cert.pem", "/etc/ssl/certs/key.pem")
}

func (p *EventProxy) Close(ctx context.Context) error {
	var err error
	select {
	case <-ctx.Done():
		err = p.server.Close()
	default:
		err = p.server.Shutdown(ctx)
	}
	if err != nil {
		return fmt.Errorf("failed to shutdown proxy server: %w", err)
	}

	return nil
}

func (p *EventProxy) proxyHandler(transport *http.Transport) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		zap.L().Info("Forwarding request", zap.String("url", r.URL.String()), zap.String("method", r.Method))
		handleHTTP(w, r, transport)
	}
}

// handleHTTP handles regular HTTP requests
func handleHTTP(w http.ResponseWriter, r *http.Request, transport *http.Transport) {
	zap.L().Info("[EVENT] handle HTTP request", zap.String("url", r.URL.String()))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, err := w.Write([]byte(`{"event_ack":true}`))
	if err != nil {
		zap.L().Error("Failed to write response", zap.Error(err))
	}
	return
}
