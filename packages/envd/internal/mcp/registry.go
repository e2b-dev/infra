package mcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	mcppb "github.com/e2b-dev/infra/packages/envd/internal/services/spec/mcp"
)

// ServiceRegistry manages MCP tool registration from Connect RPC services
type ServiceRegistry struct {
	mcpServer      *server.MCPServer
	tools          map[string]*ToolHandler
	customHandlers map[string]CustomToolHandler
}

// ToolHandler wraps a Connect RPC method invocation
type ToolHandler struct {
	ServiceName  string
	MethodName   string
	Tool         mcp.Tool
	IsStreaming  bool
	InputFactory func() proto.Message
	Invoker      func(ctx context.Context, req proto.Message) (any, error)
}

// NewServiceRegistry creates a new service registry
func NewServiceRegistry() *ServiceRegistry {
	return &ServiceRegistry{
		tools:          make(map[string]*ToolHandler),
		customHandlers: make(map[string]CustomToolHandler),
	}
}

// RegisterMethod registers a single Connect RPC method as an MCP tool
func (r *ServiceRegistry) RegisterMethod(
	serviceName string,
	methodName string,
	description string,
	inputFactory func() proto.Message,
	invoker func(ctx context.Context, req proto.Message) (any, error),
	isStreaming bool,
) error {
	toolName := fmt.Sprintf("%s.%s", serviceName, methodName)

	// Generate JSON Schema from a sample message
	sampleMsg := inputFactory()
	schema := generateSchemaFromMessage(sampleMsg.ProtoReflect().Descriptor())

	tool := mcp.Tool{
		Name:        toolName,
		Description: description,
		InputSchema: schema,
	}

	r.tools[toolName] = &ToolHandler{
		ServiceName:  serviceName,
		MethodName:   methodName,
		Tool:         tool,
		IsStreaming:  isStreaming,
		InputFactory: inputFactory,
		Invoker:      invoker,
	}

	return nil
}

// RegisterCustomHandler overrides a tool with custom implementation
func (r *ServiceRegistry) RegisterCustomHandler(toolName string, handler CustomToolHandler) {
	r.customHandlers[toolName] = handler
}

// RegisterServiceFromDescriptor automatically registers all MCP-enabled methods from a service descriptor
func (r *ServiceRegistry) RegisterServiceFromDescriptor(
	serviceName string,
	serviceDesc protoreflect.ServiceDescriptor,
	methodInvokers map[string]func(ctx context.Context, req proto.Message) (any, error),
	inputFactories map[string]func() proto.Message,
) error {
	methods := serviceDesc.Methods()

	for i := 0; i < methods.Len(); i++ {
		method := methods.Get(i)

		// Only register methods with mcp.enabled = true
		if !isMCPEnabled(method) {
			continue
		}

		methodName := string(method.Name())
		toolName := fmt.Sprintf("%s.%s", serviceName, methodName)

		// Get the invoker and input factory for this method
		invoker, hasInvoker := methodInvokers[methodName]
		inputFactory, hasFactory := inputFactories[methodName]

		if !hasInvoker || !hasFactory {
			return fmt.Errorf("missing invoker or input factory for method %s", methodName)
		}

		// Extract description from method comments if available
		description := fmt.Sprintf("Call %s.%s method", serviceName, methodName)
		if comment := method.ParentFile().SourceLocations().ByDescriptor(method); comment.LeadingComments != "" {
			description = comment.LeadingComments
		}

		// Register the method
		if err := r.RegisterMethod(
			serviceName,
			methodName,
			description,
			inputFactory,
			invoker,
			method.IsStreamingServer() || method.IsStreamingClient(),
		); err != nil {
			return fmt.Errorf("failed to register method %s: %w", toolName, err)
		}
	}

	return nil
}

// BuildMCPServer creates and configures the MCP server with all registered tools
func (r *ServiceRegistry) BuildMCPServer(name, version string) *server.MCPServer {
	r.mcpServer = server.NewMCPServer(
		name,
		version,
		server.WithToolCapabilities(true),
	)

	// Register all tools with the MCP server
	for toolName, handler := range r.tools {
		// Capture for closure
		h := handler
		toolName := toolName

		r.mcpServer.AddTool(h.Tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Convert arguments to map
			args, ok := request.Params.Arguments.(map[string]any)
			if !ok {
				return mcp.NewToolResultError("invalid arguments format"), nil
			}

			// Check for custom handler override
			if customHandler, ok := r.customHandlers[toolName]; ok {
				result, err := customHandler(ctx, args)
				if err != nil {
					return mcp.NewToolResultError(err.Error()), nil
				}
				return formatResult(result), nil
			}

			// Use default handler
			return r.handleToolInvocation(h, args)
		})
	}

	return r.mcpServer
}

// handleToolInvocation executes a tool by calling its Connect RPC handler
func (r *ServiceRegistry) handleToolInvocation(handler *ToolHandler, args map[string]interface{}) (*mcp.CallToolResult, error) {
	// Create new message instance
	req := handler.InputFactory()

	// Unmarshal args into protobuf message
	if err := unmarshalArgsToProto(args, req); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to unmarshal args: %v", err)), nil
	}

	// Invoke the handler
	result, err := handler.Invoker(context.Background(), req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	return formatResult(result), nil
}

// GetMCPServer returns the built MCP server
func (r *ServiceRegistry) GetMCPServer() *server.MCPServer {
	return r.mcpServer
}

// Helper functions

// isMCPEnabled checks if a method has the mcp.enabled option set to true
func isMCPEnabled(method protoreflect.MethodDescriptor) bool {
	opts := method.Options()
	if opts == nil {
		return false
	}

	// Get the MCP extension
	if !proto.HasExtension(opts, mcppb.E_Mcp) {
		return false
	}

	mcpOpts := proto.GetExtension(opts, mcppb.E_Mcp).(*mcppb.MCPOptions)
	return mcpOpts != nil && mcpOpts.GetEnabled()
}

// generateSchemaFromMessage creates JSON Schema from protobuf message descriptor
func generateSchemaFromMessage(msgDesc protoreflect.MessageDescriptor) mcp.ToolInputSchema {
	schema := mcp.ToolInputSchema{
		Type:       "object",
		Properties: make(map[string]any),
		Required:   []string{},
	}

	fields := msgDesc.Fields()
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		fieldName := string(field.Name())

		fieldSchema := generateFieldSchemaMap(field)
		schema.Properties[fieldName] = fieldSchema

		// Mark required fields
		if field.Cardinality() == protoreflect.Required {
			schema.Required = append(schema.Required, fieldName)
		}
	}

	return schema
}

// generateFieldSchemaMap converts field to JSON Schema map
func generateFieldSchemaMap(field protoreflect.FieldDescriptor) map[string]any {
	schema := make(map[string]any)

	if field.IsList() {
		schema["type"] = "array"
		schema["items"] = generateTypeSchemaMap(field)
		return schema
	}

	if field.IsMap() {
		schema["type"] = "object"
		return schema
	}

	return generateTypeSchemaMap(field)
}

// generateTypeSchemaMap determines JSON Schema type
func generateTypeSchemaMap(field protoreflect.FieldDescriptor) map[string]any {
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
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		schema["type"] = "number"
	case protoreflect.StringKind:
		schema["type"] = "string"
	case protoreflect.BytesKind:
		schema["type"] = "string"
		schema["contentEncoding"] = "base64"
	case protoreflect.EnumKind:
		schema["type"] = "string"
		enumDesc := field.Enum()
		values := enumDesc.Values()
		var enumValues []string
		for i := 0; i < values.Len(); i++ {
			enumValues = append(enumValues, string(values.Get(i).Name()))
		}
		schema["enum"] = enumValues
	case protoreflect.MessageKind:
		schema["type"] = "object"
		// Recursively expand nested messages
		nestedSchema := generateSchemaFromMessage(field.Message())
		schema["properties"] = nestedSchema.Properties
	default:
		schema["type"] = "string"
	}

	return schema
}

// unmarshalArgsToProto converts map[string]interface{} to protobuf message
func unmarshalArgsToProto(args map[string]interface{}, msg proto.Message) error {
	// Use protobuf reflection to set fields
	refMsg := msg.ProtoReflect()
	msgDesc := refMsg.Descriptor()
	fields := msgDesc.Fields()

	for key, value := range args {
		field := fields.ByJSONName(key)
		if field == nil {
			field = fields.ByName(protoreflect.Name(key))
		}
		if field == nil {
			continue // Skip unknown fields
		}

		if err := setFieldValue(refMsg, field, value); err != nil {
			return fmt.Errorf("failed to set field %s: %w", key, err)
		}
	}

	return nil
}

// setFieldValue sets a field value using reflection
func setFieldValue(msg protoreflect.Message, field protoreflect.FieldDescriptor, value interface{}) error {
	switch field.Kind() {
	case protoreflect.BoolKind:
		if v, ok := value.(bool); ok {
			msg.Set(field, protoreflect.ValueOfBool(v))
		}
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		if v, ok := value.(float64); ok {
			msg.Set(field, protoreflect.ValueOfInt32(int32(v)))
		}
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		if v, ok := value.(float64); ok {
			msg.Set(field, protoreflect.ValueOfInt64(int64(v)))
		}
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		if v, ok := value.(float64); ok {
			msg.Set(field, protoreflect.ValueOfUint32(uint32(v)))
		}
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		if v, ok := value.(float64); ok {
			msg.Set(field, protoreflect.ValueOfUint64(uint64(v)))
		}
	case protoreflect.FloatKind:
		if v, ok := value.(float64); ok {
			msg.Set(field, protoreflect.ValueOfFloat32(float32(v)))
		}
	case protoreflect.DoubleKind:
		if v, ok := value.(float64); ok {
			msg.Set(field, protoreflect.ValueOfFloat64(v))
		}
	case protoreflect.StringKind:
		if v, ok := value.(string); ok {
			msg.Set(field, protoreflect.ValueOfString(v))
		}
	case protoreflect.EnumKind:
		if v, ok := value.(string); ok {
			enumVal := field.Enum().Values().ByName(protoreflect.Name(v))
			if enumVal != nil {
				msg.Set(field, protoreflect.ValueOfEnum(enumVal.Number()))
			}
		}
	case protoreflect.MessageKind:
		if v, ok := value.(map[string]interface{}); ok {
			nestedMsg := msg.NewField(field).Message()
			if err := unmarshalArgsToProto(v, nestedMsg.Interface()); err != nil {
				return err
			}
			msg.Set(field, protoreflect.ValueOfMessage(nestedMsg))
		}
	}

	return nil
}

// formatResult converts a result to MCP tool result
func formatResult(result any) *mcp.CallToolResult {
	switch v := result.(type) {
	case string:
		return mcp.NewToolResultText(v)
	case proto.Message:
		// Convert protobuf to JSON text
		return mcp.NewToolResultText(fmt.Sprintf("%v", v))
	case []proto.Message:
		// Format array of messages
		return mcp.NewToolResultText(fmt.Sprintf("%v", v))
	default:
		return mcp.NewToolResultText(fmt.Sprintf("%v", v))
	}
}
