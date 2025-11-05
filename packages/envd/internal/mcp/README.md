# MCP Adapter for Connect RPC

This package provides automatic MCP (Model Context Protocol) server generation from existing Connect RPC services. It wraps your existing protobuf-based Connect RPC handlers and exposes them as MCP tools. You control which methods are exposed via MCP using protobuf annotations.

## Overview

The MCP adapter allows AI models to interact with your envd services through the standardized Model Context Protocol. Only methods explicitly marked with the `mcp.enabled` option in your protobuf definitions will be exposed as MCP tools, giving you fine-grained control over your API surface.

### Features

- ✅ **Opt-in exposure**: Only methods with `option (mcp.mcp) = { enabled: true; }` are exposed
- ✅ **Automatic discovery**: Automatically discovers and registers MCP-enabled methods
- ✅ **Type-safe**: JSON Schema automatically generated from protobuf
- ✅ **Flexible customization**: Override specific methods with custom implementations
- ✅ **Streaming support**: Handles Connect RPC server streaming
- ✅ **Proto-first control**: API surface controlled via protobuf annotations
- ✅ **Multiple transports**: Supports stdio and HTTP/SSE

## Architecture

```
┌─────────────────────────────────────┐
│    AI Model (Claude, GPT, etc.)    │
└─────────────┬───────────────────────┘
              │ MCP Protocol (JSON-RPC)
┌─────────────▼───────────────────────┐
│         MCP Server                  │
│  (github.com/mark3labs/mcp-go)     │
└─────────────┬───────────────────────┘
              │
┌─────────────▼───────────────────────┐
│      ServiceRegistry                │
│   - Auto-generates tools            │
│   - Converts JSON ↔ Protobuf       │
│   - Handles custom overrides        │
└─────────────┬───────────────────────┘
              │
┌─────────────▼───────────────────────┐
│   Your Existing Connect RPC         │
│   - ProcessService                  │
│   - FilesystemService               │
│   - REST API endpoints              │
└─────────────────────────────────────┘
```

## Quick Start

### Step 1: Annotate Your Protobuf Methods

First, import the MCP proto and mark which methods should be exposed:

```protobuf
syntax = "proto3";

package process;

import "mcp/mcp.proto";

service Process {
    // This method will be exposed via MCP
    rpc List(ListRequest) returns (ListResponse) {
        option (mcp.mcp) = {
            enabled: true;
        };
    }

    // This method will NOT be exposed via MCP (no annotation)
    rpc Connect(ConnectRequest) returns (stream ConnectResponse);

    // This method is also exposed
    rpc SendSignal(SendSignalRequest) returns (SendSignalResponse) {
        option (mcp.mcp) = {
            enabled: true;
        };
    }
}
```

### Step 2: Basic Setup

```go
package main

import (
    "log"
    "github.com/e2b-dev/infra/packages/envd/internal/mcp"
)

func main() {
    // Create your existing services (already done in main.go)
    processService := processRpc.Handle(m, &processLogger, defaults)
    filesystemService := filesystemRpc.Handle(m, &fsLogger, defaults)

    // Setup MCP server - only methods with mcp.enabled = true will be exposed
    mcpServer := mcp.SetupMCPServer(processService)

    // Start MCP server on stdio (for Claude Code, cursor, etc.)
    if err := mcpServer.ServeStdio(); err != nil {
        log.Fatal(err)
    }
}
```

### Manual Registration with Full Control

```go
package main

import (
    "context"
    "log"

    "github.com/e2b-dev/infra/packages/envd/internal/mcp"
    processrpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/process"
    "google.golang.org/protobuf/proto"
)

func main() {
    registry := mcp.NewServiceRegistry()

    // Register a simple unary RPC method
    registry.RegisterMethod(
        "process",           // Service name
        "List",             // Method name
        "List all running processes in the sandbox",
        func() proto.Message { return &processrpc.ListRequest{} },
        func(ctx context.Context, req proto.Message) (any, error) {
            listReq := req.(*processrpc.ListRequest)
            // Call your actual service
            return processService.List(ctx, connectReq)
        },
        false, // Not streaming
    )

    // Register a streaming method with custom handler
    registry.RegisterCustomHandler("process.Start", func(ctx context.Context, args map[string]any) (any, error) {
        // Extract arguments from JSON
        cmd := args["cmd"].(string)

        // Build protobuf request
        req := &processrpc.StartRequest{
            Cmd: cmd,
        }

        // Call your service and handle streaming response
        stream, err := processService.Start(ctx, connectReq)
        if err != nil {
            return nil, err
        }

        // Collect streaming results
        var outputs []string
        for stream.Receive() {
            msg := stream.Msg()
            outputs = append(outputs, msg.GetOutput())
        }

        return map[string]any{
            "outputs": outputs,
            "exitCode": stream.Msg().GetExitCode(),
        }, stream.Err()
    })

    // Build MCP server
    mcpServer := registry.BuildMCPServer("envd", "0.4.2")

    // Start server
    if err := mcpServer.ServeStdio(); err != nil {
        log.Fatal(err)
    }
}
```

## Handling Streaming Methods

Connect RPC server streaming methods require special handling in MCP since MCP tools are request/response based. Here are the recommended patterns:

### Pattern 1: Collect All Results

Collect the entire stream and return as an array:

```go
registry.RegisterCustomHandler("process.Start", func(ctx context.Context, args map[string]any) (any, error) {
    stream, err := processService.Start(ctx, connectReq)
    if err != nil {
        return nil, err
    }

    var results []map[string]any
    for stream.Receive() {
        msg := stream.Msg()
        results = append(results, map[string]any{
            "output": msg.GetOutput(),
            "timestamp": msg.GetTimestamp(),
        })
    }

    return results, stream.Err()
})
```

### Pattern 2: Return Final Result Only

For streaming methods where only the final state matters:

```go
registry.RegisterCustomHandler("process.Start", func(ctx context.Context, args map[string]any) (any, error) {
    stream, err := processService.Start(ctx, connectReq)
    if err != nil {
        return nil, err
    }

    var lastMsg *processrpc.StartResponse
    for stream.Receive() {
        lastMsg = stream.Msg()
    }

    return map[string]any{
        "pid": lastMsg.GetPid(),
        "exitCode": lastMsg.GetExitCode(),
    }, stream.Err()
})
```

### Pattern 3: Progress Notifications (Advanced)

Use MCP notifications to send progress updates:

```go
// Note: Requires MCP server to support notifications
registry.RegisterCustomHandler("process.Start", func(ctx context.Context, args map[string]any) (any, error) {
    stream, err := processService.Start(ctx, connectReq)
    if err != nil {
        return nil, err
    }

    var lastMsg *processrpc.StartResponse
    for stream.Receive() {
        msg := stream.Msg()
        lastMsg = msg

        // Send progress notification
        // mcpServer.SendNotification("process/output", msg.GetOutput())
    }

    return lastMsg, stream.Err()
})
```

## Custom Method Overrides

Override any tool with custom logic while keeping the rest auto-generated:

```go
// Setup with defaults
registry := mcp.NewServiceRegistry()

// Auto-register all methods
registerAllProcessMethods(registry, processService)

// Override specific method
registry.RegisterCustomHandler("process.Start", func(ctx context.Context, args map[string]any) (any, error) {
    // Custom validation
    cmd := args["cmd"].(string)
    if strings.Contains(cmd, "rm -rf") {
        return nil, fmt.Errorf("dangerous command not allowed")
    }

    // Custom pre-processing
    if cwd, ok := args["cwd"]; !ok {
        args["cwd"] = "/workspace"
    }

    // Call actual service
    return callProcessStart(ctx, args)
})
```

## Working with Protobuf Reflection

The adapter uses protobuf reflection to automatically generate JSON Schema:

```go
// Access file descriptor from generated code
fileDesc := processrpc.File_process_process_proto

// Iterate over services
services := fileDesc.Services()
for i := 0; i < services.Len(); i++ {
    service := services.Get(i)

    // Iterate over methods
    methods := service.Methods()
    for j := 0; j < methods.Len(); j++ {
        method := methods.Get(j)

        // Register each method automatically
        registry.RegisterMethod(
            string(service.Name()),
            string(method.Name()),
            extractDescription(method),
            createMessageFactory(method.Input()),
            createInvoker(service, method),
            method.IsStreamingServer(),
        )
    }
}
```

## Integration with Existing Server

### Option 1: Separate MCP Binary

Create a separate `envd-mcp` binary that connects to your existing HTTP server:

```go
// cmd/envd-mcp/main.go
func main() {
    baseURL := "http://localhost:49983"

    adapter := mcp.NewConnectRPCAdapter(baseURL)

    // Register services by making HTTP calls
    adapter.RegisterServiceFromFileDescriptor(
        processrpc.File_process_process_proto,
        "ProcessService",
    )

    // Start MCP server
    adapter.Start(context.Background(), "stdio")
}
```

### Option 2: Embedded in Main Server

Add MCP as an additional transport in your existing `main.go`:

```go
// main.go
func main() {
    // ... existing setup ...

    // Check if running in MCP mode
    if os.Getenv("MCP_MODE") == "true" {
        mcpServer := mcp.SetupMCPServer(processService)
        if err := mcpServer.ServeStdio(); err != nil {
            log.Fatal(err)
        }
        return
    }

    // ... continue with normal HTTP server ...
}
```

Then run with: `MCP_MODE=true ./envd`

## Testing

Test your MCP tools using the MCP Inspector:

```bash
# Install MCP Inspector
npm install -g @modelcontextprotocol/inspector

# Run your MCP server
mcp-inspector ./envd --mcp-mode

# Or test via stdio directly
echo '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | ./envd --mcp-mode
```

## Configuration Options

### Transport Options

**Stdio** (recommended for local development and CLI tools):
```go
mcpServer.ServeStdio()
```

**HTTP with SSE** (for web-based clients):
```go
// Coming soon - requires additional HTTP setup
mcpServer.ServeHTTP(":8080")
```

### Custom Stream Collectors

Customize how streaming responses are collected:

```go
type CustomCollector struct{}

func (c *CustomCollector) CollectStream(ctx context.Context, stream io.Reader) (any, error) {
    // Your custom logic
    return result, nil
}

adapter := mcp.NewConnectRPCAdapter(baseURL)
adapter.streamCollector = &CustomCollector{}
```

## Examples

### Complete Process Service Registration

```go
func RegisterProcessService(registry *mcp.ServiceRegistry, svc *process.Service) {
    // List
    registry.RegisterMethod("process", "List",
        "List all running processes",
        func() proto.Message { return &processrpc.ListRequest{} },
        func(ctx context.Context, req proto.Message) (any, error) {
            return svc.List(ctx, connect.NewRequest(req.(*processrpc.ListRequest)))
        },
        false,
    )

    // Start (with custom handler for streaming)
    registry.RegisterMethod("process", "Start",
        "Start a new process with streaming output",
        func() proto.Message { return &processrpc.StartRequest{} },
        nil, // Placeholder, will use custom handler
        true,
    )

    registry.RegisterCustomHandler("process.Start", func(ctx context.Context, args map[string]any) (any, error) {
        req := &processrpc.StartRequest{
            Cmd: args["cmd"].(string),
        }

        stream, err := svc.Start(ctx, connect.NewRequest(req))
        if err != nil {
            return nil, err
        }

        // Collect all output
        var outputs []string
        for stream.Receive() {
            outputs = append(outputs, stream.Msg().GetOutput())
        }

        return map[string]any{"outputs": outputs}, stream.Err()
    })

    // SendSignal
    registry.RegisterMethod("process", "SendSignal",
        "Send a signal to a process (SIGTERM, SIGKILL)",
        func() proto.Message { return &processrpc.SendSignalRequest{} },
        func(ctx context.Context, req proto.Message) (any, error) {
            return svc.SendSignal(ctx, connect.NewRequest(req.(*processrpc.SendSignalRequest)))
        },
        false,
    )
}
```

## Troubleshooting

### JSON Schema Generation Issues

If you see schema errors, check that your protobuf types are supported. Currently supported:
- ✅ All scalar types (int32, int64, string, bool, etc.)
- ✅ Enums
- ✅ Nested messages
- ✅ Repeated fields (arrays)
- ✅ Maps
- ⚠️  Any/OneOf - requires custom handling

### Streaming Not Working

Make sure:
1. You've registered a custom handler for streaming methods
2. The stream is being properly consumed before returning
3. Check for context cancellation

### Type Conversion Errors

The adapter converts JSON to protobuf using reflection. If you see conversion errors:
- Ensure JSON field names match proto field names (or use `json_name` option)
- Check that numeric types match (JSON numbers are float64 by default)
- Verify enum values are passed as strings

## Future Enhancements

- [ ] Automatic streaming to MCP notifications
- [ ] REST API endpoint exposure as MCP resources
- [ ] Protobuf custom options for MCP configuration
- [ ] HTTP/SSE transport support
- [ ] Automatic OpenAPI to MCP conversion
- [ ] Bidirectional streaming support
- [ ] Authentication/authorization integration

## References

- [Model Context Protocol Specification](https://modelcontextprotocol.io/specification/2025-06-18/)
- [Connect RPC Documentation](https://connectrpc.com/docs/)
- [mark3labs/mcp-go Library](https://github.com/mark3labs/mcp-go)
