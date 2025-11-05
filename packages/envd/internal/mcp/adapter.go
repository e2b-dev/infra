package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// ConnectRPCAdapter wraps existing Connect RPC handlers and exposes them as MCP tools
type ConnectRPCAdapter struct {
	mcpServer       *server.MCPServer
	httpClient      *http.Client
	baseURL         string
	tools           []mcp.Tool
	customHandlers  map[string]CustomToolHandler
	streamCollector StreamCollector
}

// CustomToolHandler allows overriding specific tool implementations
type CustomToolHandler func(ctx context.Context, args map[string]any) (any, error)

// StreamCollector defines how to handle streaming RPC responses
type StreamCollector interface {
	CollectStream(ctx context.Context, stream io.Reader) (any, error)
}

// DefaultStreamCollector collects all streaming messages into an array
type DefaultStreamCollector struct{}

func (d *DefaultStreamCollector) CollectStream(ctx context.Context, stream io.Reader) (any, error) {
	var results []json.RawMessage
	decoder := json.NewDecoder(stream)

	for {
		var msg json.RawMessage
		if err := decoder.Decode(&msg); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		results = append(results, msg)
	}

	return results, nil
}

// NewConnectRPCAdapter creates an MCP adapter for existing Connect RPC services
func NewConnectRPCAdapter(baseURL string) *ConnectRPCAdapter {
	return &ConnectRPCAdapter{
		httpClient:      &http.Client{},
		baseURL:         strings.TrimSuffix(baseURL, "/"),
		tools:           []mcp.Tool{},
		customHandlers:  make(map[string]CustomToolHandler),
		streamCollector: &DefaultStreamCollector{},
	}
}

// RegisterService discovers and registers all MCP-enabled methods from a Connect RPC service
func (a *ConnectRPCAdapter) RegisterService(serviceName string, serviceDesc protoreflect.ServiceDescriptor) error {
	methods := serviceDesc.Methods()

	for i := 0; i < methods.Len(); i++ {
		method := methods.Get(i)

		// Only register methods with mcp.enabled = true
		if !isMCPEnabled(method) {
			continue
		}

		toolName := fmt.Sprintf("%s.%s", serviceName, method.Name())

		tool := mcp.Tool{
			Name:        toolName,
			Description: extractDescription(method),
			InputSchema: a.generateJSONSchema(method.Input()),
		}

		a.tools = append(a.tools, tool)
	}

	return nil
}

// RegisterServiceFromFileDescriptor registers a service using its file descriptor
func (a *ConnectRPCAdapter) RegisterServiceFromFileDescriptor(fd protoreflect.FileDescriptor, serviceName string) error {
	services := fd.Services()

	for i := 0; i < services.Len(); i++ {
		service := services.Get(i)
		if string(service.Name()) == serviceName {
			return a.RegisterService(serviceName, service)
		}
	}

	return fmt.Errorf("service %s not found in file descriptor", serviceName)
}

// RegisterCustomHandler overrides the default handler for a specific tool
func (a *ConnectRPCAdapter) RegisterCustomHandler(toolName string, handler CustomToolHandler) {
	a.customHandlers[toolName] = handler
}

// generateJSONSchema converts a protobuf message descriptor to JSON Schema
func (a *ConnectRPCAdapter) generateJSONSchema(msgDesc protoreflect.MessageDescriptor) mcp.ToolInputSchema {
	schema := mcp.ToolInputSchema{
		Type:       "object",
		Properties: make(map[string]any),
		Required:   []string{},
	}

	fields := msgDesc.Fields()
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		fieldName := string(field.Name())

		fieldSchema := a.generateFieldSchema(field)
		schema.Properties[fieldName] = fieldSchema

		// In proto3, all fields are optional by default, but we can check cardinality
		if field.Cardinality() == protoreflect.Required {
			schema.Required = append(schema.Required, fieldName)
		}
	}

	return schema
}

// generateFieldSchema converts a protobuf field descriptor to JSON Schema property
func (a *ConnectRPCAdapter) generateFieldSchema(field protoreflect.FieldDescriptor) map[string]any {
	schema := make(map[string]any)

	// Handle repeated fields
	if field.IsList() {
		schema["type"] = "array"
		schema["items"] = a.generateTypeSchema(field)
		return schema
	}

	// Handle maps
	if field.IsMap() {
		schema["type"] = "object"
		schema["additionalProperties"] = a.generateTypeSchema(field.MapValue())
		return schema
	}

	// Handle singular fields
	return a.generateTypeSchema(field)
}

// generateTypeSchema determines the JSON Schema type for a protobuf field
func (a *ConnectRPCAdapter) generateTypeSchema(field protoreflect.FieldDescriptor) map[string]any {
	schema := make(map[string]any)

	switch field.Kind() {
	case protoreflect.BoolKind:
		schema["type"] = "boolean"
	case protoreflect.Int32Kind, protoreflect.Int64Kind,
		protoreflect.Sint32Kind, protoreflect.Sint64Kind,
		protoreflect.Sfixed32Kind, protoreflect.Sfixed64Kind:
		schema["type"] = "integer"
	case protoreflect.Uint32Kind, protoreflect.Uint64Kind,
		protoreflect.Fixed32Kind, protoreflect.Fixed64Kind:
		schema["type"] = "integer"
		schema["minimum"] = 0
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		schema["type"] = "number"
	case protoreflect.StringKind:
		schema["type"] = "string"
	case protoreflect.BytesKind:
		schema["type"] = "string"
		schema["contentEncoding"] = "base64"
	case protoreflect.EnumKind:
		schema["type"] = "string"
		// Could enumerate enum values here
		enumDesc := field.Enum()
		values := enumDesc.Values()
		var enumValues []string
		for i := 0; i < values.Len(); i++ {
			enumValues = append(enumValues, string(values.Get(i).Name()))
		}
		schema["enum"] = enumValues
	case protoreflect.MessageKind:
		// Nested message - recursively generate schema
		schema["type"] = "object"
		// For simplicity, we don't recursively expand nested messages
		// You could call generateJSONSchema(field.Message()) here
	default:
		schema["type"] = "string"
	}

	// Add description from comments if available
	if desc := extractFieldDescription(field); desc != "" {
		schema["description"] = desc
	}

	return schema
}

// callConnectRPC makes an HTTP request to the Connect RPC endpoint
func (a *ConnectRPCAdapter) callConnectRPC(ctx context.Context, serviceName, methodName string, inputMsg proto.Message) (any, error) {
	// Construct Connect RPC endpoint URL
	endpoint := fmt.Sprintf("%s/%s/%s", a.baseURL, serviceName, methodName)

	// Marshal request to JSON
	reqBody, err := protojson.Marshal(inputMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(string(reqBody)))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Execute request
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Check for errors
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed with status %d", resp.StatusCode)
	}

	// Check if response is streaming
	if strings.Contains(resp.Header.Get("Content-Type"), "application/connect+json") {
		// Handle streaming response
		return a.streamCollector.CollectStream(ctx, resp.Body)
	}

	// Parse response
	var result json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result, nil
}

// handleToolCall is the generic handler for all MCP tool calls
func (a *ConnectRPCAdapter) handleToolCall(ctx context.Context, toolName string, args map[string]any) (*mcp.CallToolResult, error) {
	// Check for custom handler
	if handler, ok := a.customHandlers[toolName]; ok {
		result, err := handler(ctx, args)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		return mcp.NewToolResultText(fmt.Sprintf("%v", result)), nil
	}

	// Default handler: proxy to Connect RPC
	parts := strings.Split(toolName, ".")
	if len(parts) != 2 {
		return mcp.NewToolResultError(fmt.Sprintf("invalid tool name format: %s", toolName)), nil
	}

	serviceName := parts[0]
	methodName := parts[1]

	// TODO: We need the actual protobuf message type here to unmarshal properly
	// For now, we'll pass the raw JSON
	_ = serviceName
	_ = methodName

	return mcp.NewToolResultText("Not implemented yet - needs message type resolution"), nil
}

// Start initializes and starts the MCP server
func (a *ConnectRPCAdapter) Start(ctx context.Context, transport string) error {
	a.mcpServer = server.NewMCPServer(
		"envd-mcp-server",
		"1.0.0",
		server.WithToolCapabilities(true),
	)

	// Register all tools
	for _, tool := range a.tools {
		toolCopy := tool // Capture for closure
		a.mcpServer.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Convert arguments to map
			args, ok := request.Params.Arguments.(map[string]any)
			if !ok {
				return mcp.NewToolResultError("invalid arguments format"), nil
			}
			return a.handleToolCall(ctx, toolCopy.Name, args)
		})
	}

	// Start server based on transport
	switch transport {
	case "stdio":
		return server.ServeStdio(a.mcpServer)
	case "sse":
		// HTTP SSE transport - requires additional setup
		return fmt.Errorf("SSE transport not yet implemented")
	default:
		return fmt.Errorf("unsupported transport: %s", transport)
	}
}

// GetMCPServer returns the underlying MCP server for advanced usage
func (a *ConnectRPCAdapter) GetMCPServer() *server.MCPServer {
	return a.mcpServer
}

// Helper functions

func extractDescription(method protoreflect.MethodDescriptor) string {
	// TODO: Extract from protobuf comments/options
	return fmt.Sprintf("Call %s method", method.Name())
}

func extractFieldDescription(field protoreflect.FieldDescriptor) string {
	// TODO: Extract from protobuf comments/options
	return ""
}

// ConvertProtoFileDescriptor converts protobuf FileDescriptorProto to FileDescriptor
func ConvertProtoFileDescriptor(fdProto []byte) (protoreflect.FileDescriptor, error) {
	fd, err := protodesc.NewFile(nil, nil)
	if err != nil {
		return nil, err
	}
	return fd, nil
}
