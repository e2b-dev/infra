package events

import (
	"context"
	"fmt"
	"net/http"
)

// SandboxEventServer handles outbound HTTP requests from sandboxes calling the event.e2b.com endpoint
type SandboxEventProxy struct {
	server *http.Server
}

func NewSandboxEventProxy(port uint, store SandboxEventStore, handlers ...SandboxEventHandler) *SandboxEventProxy {
	mux := http.NewServeMux()

	handlers = append(handlers, NewDefaultSandboxEventHandler(store))
	for _, handler := range handlers {
		mux.HandleFunc(handler.Path(), handler.HandlerFunc)
	}

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	return &SandboxEventProxy{
		server: server,
	}
}

func (p *SandboxEventProxy) Start() error {
	return p.server.ListenAndServe()
}

func (p *SandboxEventProxy) Close(ctx context.Context) error {
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
