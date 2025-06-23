package event

import (
	"context"
	"fmt"
	"net/http"

	"go.uber.org/zap"
)

// EventServer handles outbound HTTP requests from sandboxes calling the event.e2b.com endpoint
type EventServer struct {
	server *http.Server
}

func validateHeaders(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sandboxID := r.Header.Get("E2B_SANDBOX_ID")
		teamID := r.Header.Get("E2B_TEAM_ID")

		if sandboxID == "" || teamID == "" {
			http.Error(w, "missing required headers", http.StatusBadRequest)
			return
		}

		next.ServeHTTP(w, r)
	}
}

func NewEventServer(port uint, handlers []EventHandler) *EventServer {
	mux := http.NewServeMux()

	for _, handler := range handlers {
		mux.HandleFunc(handler.Path(), validateHeaders(handler.HandlerFunc))
	}

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	return &EventServer{
		server: server,
	}
}

func (p *EventServer) Start() error {
	zap.L().Info("Starting event server")
	return p.server.ListenAndServe()
}

func (p *EventServer) Close(ctx context.Context) error {
	var err error
	select {
	case <-ctx.Done():
		err = p.server.Close()
	default:
		err = p.server.Shutdown(ctx)
	}
	if err != nil {
		return fmt.Errorf("failed to shutdown event server: %w", err)
	}

	return nil
}
