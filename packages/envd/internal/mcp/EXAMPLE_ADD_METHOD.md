# Example: Adding Another MCP-Enabled Method

This guide shows how to expose additional RPC methods via MCP using the annotation system.

## Scenario: Enable the SendSignal Method

Let's say you want to expose the `SendSignal` method so AI assistants can send signals to processes.

### Step 1: Update the Protobuf Definition

Edit `spec/process/process.proto`:

```protobuf
service Process {
    rpc List(ListRequest) returns (ListResponse) {
        option (mcp.mcp) = {
            enabled: true;
        };
    }

    // Add the annotation here
    rpc SendSignal(SendSignalRequest) returns (SendSignalResponse) {
        option (mcp.mcp) = {
            enabled: true;
        };
    }

    // Other methods remain unexposed unless annotated
    rpc Connect(ConnectRequest) returns (stream ConnectResponse);
    rpc Start(StartRequest) returns (stream StartResponse);
    // ...
}
```

### Step 2: Regenerate Protobuf Code

Run your protobuf generation command:

```bash
# Example - adjust based on your setup
buf generate
# or
make generate-proto
```

This regenerates the Go code with the new annotation information.

### Step 3: Update Your Service Registration Code

If using the automatic registration approach, update your invoker map:

```go
// In your MCP setup code (e.g., internal/mcp/example_integration.go or main.go)

methodInvokers := map[string]func(ctx context.Context, req proto.Message) (any, error){
    "List": func(ctx context.Context, req proto.Message) (any, error) {
        listReq := req.(*processrpc.ListRequest)
        return processSvc.List(ctx, connect.NewRequest(listReq))
    },
    // Add the SendSignal invoker
    "SendSignal": func(ctx context.Context, req proto.Message) (any, error) {
        signalReq := req.(*processrpc.SendSignalRequest)
        return processSvc.SendSignal(ctx, connect.NewRequest(signalReq))
    },
}

// Input factories already include SendSignal, so no change needed
inputFactories := map[string]func() proto.Message{
    "List":       func() proto.Message { return &processrpc.ListRequest{} },
    "SendSignal": func() proto.Message { return &processrpc.SendSignalRequest{} },
    // ... other methods
}

// Register with automatic filtering
registry.RegisterServiceFromDescriptor("process", serviceDesc, methodInvokers, inputFactories)
```

### Step 4: Test the New Tool

Build and run your MCP server:

```bash
go build .
./envd --mcp-mode  # or however you start your MCP server
```

Test with MCP Inspector or your AI assistant:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/list"
}
```

Response should now include both tools:
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "tools": [
      {
        "name": "process.List",
        "description": "List all running processes in the sandbox",
        "inputSchema": { ... }
      },
      {
        "name": "process.SendSignal",
        "description": "Send a signal to a running process",
        "inputSchema": {
          "type": "object",
          "properties": {
            "process": { ... },
            "signal": {
              "type": "string",
              "enum": ["SIGNAL_SIGTERM", "SIGNAL_SIGKILL", ...]
            }
          }
        }
      }
    ]
  }
}
```

## Verify with Test

Update the test to verify both methods are enabled:

```go
func TestMCPAnnotationFiltering(t *testing.T) {
    // ... existing test setup ...

    // Verify that we now have 2 MCP-enabled methods
    if mcpEnabledCount != 2 {
        t.Errorf("Expected 2 MCP-enabled methods, got %d", mcpEnabledCount)
    }

    // Verify SendSignal is enabled
    sendSignalMethod := serviceDesc.Methods().ByName("SendSignal")
    if !isMCPEnabled(sendSignalMethod) {
        t.Error("SendSignal method should be MCP-enabled")
    }
}
```

## Best Practices

1. **Start Small**: Enable methods one at a time and test thoroughly
2. **Document Intent**: Add comments explaining why a method is/isn't MCP-enabled
3. **Security Review**: Consider security implications before exposing methods
4. **Streaming Methods**: For streaming methods, you may need custom handlers:

```go
registry.RegisterCustomHandler("process.Start", func(ctx context.Context, args map[string]any) (any, error) {
    // Custom logic to handle streaming
    // Collect stream results and return aggregated data
})
```

5. **Test Coverage**: Add tests for each newly enabled method

## Removing MCP Access

To remove MCP access from a method:

1. Remove the `option (mcp.mcp) = { enabled: true; }` from the .proto file
2. Regenerate protobuf code
3. Restart the MCP server

The method will automatically be filtered out.
