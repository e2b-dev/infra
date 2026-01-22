package mcp

import (
	"fmt"

	"github.com/mark3labs/mcp-go/server"

	processrpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/process"
)

// Setup registers all MCP-enabled methods from envd services.
func Setup(mcpServer *server.MCPServer, port int) {
	mw := New(mcpServer, fmt.Sprintf("http://localhost:%d", port))

	// Auto-register all MCP-enabled methods from proto descriptors
	mw.Register(processrpc.File_process_process_proto)
}
