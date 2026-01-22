# MCP Middleware for Connect RPC

Universal middleware that exposes Connect RPC methods via MCP based on proto annotations.

## Usage

### Method annotation

```protobuf
import "mcp/mcp.proto";

rpc Start(StartRequest) returns (stream StartResponse) {
    option (mcp.mcp) = {
        enabled: true;
        description: "Start a process and stream output";
    };
}
```

### Field annotations

```protobuf
message StartRequest {    
    // Process configuration (cmd, args, envs, cwd)
    ProcessConfig process = 1 [
        (mcp.mcp_field) = { description: "Process configuration"; }
    ];

    // Hidden from MCP, uses default
    optional bool stdin = 4 [
        (mcp.mcp_field) = { 
            hidden: true; 
            default_value: "false"; 
        }
    ];
}
```

Regenerate proto and the middleware handles the rest.

## Method Options (mcp.mcp)

- `enabled` - Enable MCP exposure for this method
- `description` - Tool description (overrides proto comments)

## Field Options (mcp.mcp_field)

- `description` - Argument description for the MCP tool schema
- `default_value` - Default value as JSON (e.g. `"false"`, `"\"hello\""`, `"123"`)
- `hidden` - If true, use default but don't expose in MCP tool schema

## Streaming

Streaming methods forward events via MCP notifications as they arrive.
Events are passed through with the same structure as Connect RPC.

## Unary

Unary methods return the response JSON directly.
