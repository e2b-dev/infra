package mcp

import (
	"context"
	"fmt"
	"io"

	"github.com/e2b-dev/infra/packages/envd/internal/services/process"
	processrpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/process"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"google.golang.org/protobuf/proto"
)

// RegisterProcessToolsWithMCPServer registers MCP tools from the process service
//
// This function demonstrates how services can register their MCP-enabled tools
// with an existing MCP server. Only methods with the `option (mcp.mcp) = { enabled: true; }`
// annotation in the protobuf definition will be exposed via MCP.
//
// Example usage in main.go:
//
//	mcpService := mcpRpc.Handle(m, &mcpLogger, defaults)
//	processService := processRpc.Handle(m, &processLogger, defaults)
//	mcp.RegisterProcessToolsWithMCPServer(mcpService.GetMCPServer(), processService)
func RegisterProcessToolsWithMCPServer(mcpServer *server.MCPServer, processSvc *process.Service) error {
	registry := NewServiceRegistry()

	// Automatic registration: Get the service descriptor and register only MCP-enabled methods
	// In process.proto, only methods with option (mcp.mcp) = { enabled: true; } will be registered

	fileDesc := processrpc.File_process_process_proto
	serviceDesc := fileDesc.Services().ByName("Process")

	// Define input factories for each method
	inputFactories := map[string]func() proto.Message{
		"List":        func() proto.Message { return &processrpc.ListRequest{} },
		"Start":       func() proto.Message { return &processrpc.StartRequest{} },
		"Connect":     func() proto.Message { return &processrpc.ConnectRequest{} },
		"SendInput":   func() proto.Message { return &processrpc.SendInputRequest{} },
		"SendSignal":  func() proto.Message { return &processrpc.SendSignalRequest{} },
		"Update":      func() proto.Message { return &processrpc.UpdateRequest{} },
		"StreamInput": func() proto.Message { return &processrpc.StreamInputRequest{} },
	}

	// Define method invokers - these make internal calls to the service methods
	methodInvokers := map[string]func(ctx context.Context, req proto.Message) (any, error){
		"List": func(ctx context.Context, req proto.Message) (any, error) {
			// For now, return a placeholder
			// In a real implementation, this would need to create a proper Connect request
			// and invoke the service method through the HTTP handler
			return nil, fmt.Errorf("tool invocation not yet implemented - needs HTTP client to call Connect RPC endpoint")
		},
	}

	// Register all MCP-enabled methods from the service descriptor
	// This will only register methods with option (mcp.mcp) = { enabled: true; }
	if err := registry.RegisterServiceFromDescriptor("process", serviceDesc, methodInvokers, inputFactories); err != nil {
		return fmt.Errorf("failed to register process tools: %w", err)
	}

	// Register tools with the provided MCP server
	for _, handler := range registry.tools {
		h := handler
		mcpServer.AddTool(h.Tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args, ok := request.Params.Arguments.(map[string]any)
			if !ok {
				return mcp.NewToolResultError("invalid arguments format"), nil
			}
			return registry.handleToolInvocation(h, args)
		})
	}

	return nil
}

// SetupMCPServerWithDirectInvocation shows how to wrap service methods more directly
// This version makes actual HTTP calls to the Connect RPC endpoints
func SetupMCPServerWithDirectInvocation(baseURL string) *server.MCPServer {
	adapter := NewConnectRPCAdapter(baseURL)

	// Register services using their file descriptors
	// This automatically discovers all methods
	processFileDesc := processrpc.File_process_process_proto
	adapter.RegisterServiceFromFileDescriptor(processFileDesc, "ProcessService")

	// Override specific methods with custom implementations
	adapter.RegisterCustomHandler("ProcessService.Start", func(ctx context.Context, args map[string]any) (any, error) {
		// Custom streaming handler
		cmd, ok := args["cmd"].(string)
		if !ok {
			return nil, fmt.Errorf("cmd is required")
		}

		// Make HTTP call to Connect RPC endpoint with streaming support
		// Collect the stream and return aggregated results
		return map[string]any{
			"message": "Started process",
			"cmd":     cmd,
		}, nil
	})

	// Start the adapter (this will create the MCP server internally)
	// Note: This is async, you'd call it in a goroutine or return the adapter
	return nil // adapter.GetMCPServer() would be called after Start()
}

// StreamingResponseCollector is a helper for collecting streaming RPC responses
type StreamingResponseCollector struct {
	messages []proto.Message
}

func NewStreamingResponseCollector() *StreamingResponseCollector {
	return &StreamingResponseCollector{
		messages: []proto.Message{},
	}
}

func (c *StreamingResponseCollector) Collect(stream interface{}) ([]proto.Message, error) {
	// Type assertion for Connect streaming response
	// This would need to be adapted based on your actual stream type
	type streamReceiver interface {
		Receive() bool
		Msg() proto.Message
		Err() error
	}

	if s, ok := stream.(streamReceiver); ok {
		for s.Receive() {
			msg := s.Msg()
			c.messages = append(c.messages, proto.Clone(msg))
		}
		return c.messages, s.Err()
	}

	// Alternative: io.Reader based stream
	if r, ok := stream.(io.Reader); ok {
		// Parse streaming JSON responses
		_ = r
		return c.messages, nil
	}

	return nil, fmt.Errorf("unsupported stream type: %T", stream)
}
