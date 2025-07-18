package event

import (
	"context"
	"fmt"
	"net/http"
)

// SandboxEventServer handles outbound HTTP requests from sandboxes calling the event.e2b.com endpoint
type SandboxEventServer struct {
	server *http.Server
}

func NewSandboxEventServer(port uint, handlers []EventHandler) *SandboxEventServer {
	mux := http.NewServeMux()

	for _, handler := range handlers {
		mux.HandleFunc(handler.Path(), handler.HandlerFunc)
	}

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	return &SandboxEventServer{
		server: server,
	}
}

func (p *SandboxEventServer) Start() error {
	return p.server.ListenAndServe()
}

func (p *SandboxEventServer) Close(ctx context.Context) error {
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
