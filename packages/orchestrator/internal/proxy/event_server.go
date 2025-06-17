package proxy

import (
	"context"
	"fmt"
	"net/http"

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
	p.server.Handler = http.HandlerFunc(p.proxyHandler())

	return p.server.ListenAndServe()
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

func (p *EventProxy) proxyHandler() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		zap.L().Info("Forwarding request", zap.String("url", r.URL.String()), zap.String("method", r.Method))
		handleHTTP(w, r)
	}
}

// handleHTTP handles regular HTTP requests
func handleHTTP(w http.ResponseWriter, r *http.Request) {
	zap.L().Info("[EVENT] handle event HTTP request", zap.String("url", r.URL.String()))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, err := w.Write([]byte(`{"event_ack":true}`))
	if err != nil {
		zap.L().Error("Failed to write response", zap.Error(err))
	}
	return
}
