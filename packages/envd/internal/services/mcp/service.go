package mcp

import (
	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
	"github.com/go-chi/chi/v5"
	"github.com/mark3labs/mcp-go/server"
	"github.com/rs/zerolog"
)

// Service wraps the MCP server functionality
type Service struct {
	logger    *zerolog.Logger
	mcpServer *server.MCPServer
	sseServer *server.SSEServer
}

// newService creates a new MCP service
func newService(l *zerolog.Logger) *Service {
	// Create a basic MCP server
	// Tools will be registered separately by services that want to expose MCP capabilities
	mcpServer := server.NewMCPServer(
		"envd-mcp-server",
		"0.4.2",
		server.WithToolCapabilities(true),
	)

	// Create SSE server for HTTP transport
	// The SSE server defaults to /sse and /message endpoints, so when mounted at /mcp
	// it will respond to /mcp/sse (GET for SSE stream) and /mcp/message (POST for messages)
	sseServer := server.NewSSEServer(mcpServer)

	return &Service{
		logger:    l,
		mcpServer: mcpServer,
		sseServer: sseServer,
	}
}

// Handle registers the MCP service HTTP endpoints and returns the service
func Handle(mux *chi.Mux, l *zerolog.Logger, defaults *execcontext.Defaults) *Service {
	service := newService(l)

	// Register individual handlers for SSE and message endpoints
	// chi.Mount strips the prefix, but SSEServer expects the full path
	// So we handle the endpoints directly
	mux.Handle("/mcp/sse", service.sseServer.SSEHandler())
	mux.Handle("/mcp/message", service.sseServer.MessageHandler())

	l.Info().Msg("MCP service registered at /mcp (endpoints: /mcp/sse, /mcp/message)")

	return service
}

// GetMCPServer returns the underlying MCP server for advanced usage
func (s *Service) GetMCPServer() *server.MCPServer {
	return s.mcpServer
}

// GetSSEServer returns the underlying SSE server for advanced usage
func (s *Service) GetSSEServer() *server.SSEServer {
	return s.sseServer
}
