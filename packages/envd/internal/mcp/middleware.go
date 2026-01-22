package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"

	mcppb "github.com/e2b-dev/infra/packages/envd/internal/services/spec/mcp"
)

// Middleware auto-registers MCP-enabled methods from proto file descriptors.
type Middleware struct {
	mcpServer  *server.MCPServer
	httpClient *http.Client
	baseURL    string
	marshaler  protojson.MarshalOptions
}

// New creates a middleware that auto-discovers MCP-enabled methods.
func New(mcpServer *server.MCPServer, baseURL string) *Middleware {
	return &Middleware{
		mcpServer:  mcpServer,
		httpClient: &http.Client{},
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		marshaler:  protojson.MarshalOptions{Indent: "  ", EmitUnpopulated: false},
	}
}

// Register discovers all MCP-enabled methods from a file descriptor and registers them.
func (m *Middleware) Register(fileDesc protoreflect.FileDescriptor) {
	services := fileDesc.Services()
	for i := 0; i < services.Len(); i++ {
		svc := services.Get(i)
		m.registerService(svc)
	}
}

func (m *Middleware) registerService(svc protoreflect.ServiceDescriptor) {
	methods := svc.Methods()
	for i := 0; i < methods.Len(); i++ {
		method := methods.Get(i)
		if !mcpEnabled(method) {
			continue
		}
		m.registerMethod(svc, method)
	}
}

func (m *Middleware) registerMethod(svc protoreflect.ServiceDescriptor, method protoreflect.MethodDescriptor) {
	svcName := string(svc.FullName())
	methodName := string(method.Name())
	toolName := fmt.Sprintf("%s.%s", svc.Name(), methodName)

	// Get MCP method options
	mcpOpts := getMCPOptions(method)

	// Collect field options from input message
	fieldOpts := collectFieldOptions(method.Input())

	tool := mcp.Tool{
		Name:        toolName,
		Description: extractDesc(method, mcpOpts),
		InputSchema: buildSchema(method.Input(), fieldOpts),
	}

	isStreaming := method.IsStreamingServer() || method.IsStreamingClient()

	m.mcpServer.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, ok := req.Params.Arguments.(map[string]any)
		if !ok {
			args = make(map[string]any)
		}

		// Apply defaults for missing args (including hidden ones)
		applyDefaults(args, fieldOpts)

		inputMsg := dynamicpb.NewMessage(method.Input())
		if err := argsToProto(args, inputMsg); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("parse error: %v", err)), nil
		}

		if isStreaming {
			return m.callStreaming(ctx, svcName, methodName, inputMsg, method.Output())
		}
		return m.callUnary(ctx, svcName, methodName, inputMsg)
	})
}

// getMCPOptions extracts MCPOptions from method descriptor.
func getMCPOptions(m protoreflect.MethodDescriptor) *mcppb.MCPOptions {
	opts := m.Options()
	if opts == nil || !proto.HasExtension(opts, mcppb.E_Mcp) {
		return &mcppb.MCPOptions{}
	}
	ext := proto.GetExtension(opts, mcppb.E_Mcp).(*mcppb.MCPOptions)
	if ext == nil {
		return &mcppb.MCPOptions{}
	}
	return ext
}

// fieldConfig holds MCP options for a field.
type fieldConfig struct {
	Description  string
	DefaultValue string
	Hidden       bool
}

// collectFieldOptions extracts MCP field options from a message descriptor.
func collectFieldOptions(msg protoreflect.MessageDescriptor) map[string]*fieldConfig {
	result := make(map[string]*fieldConfig)
	fields := msg.Fields()
	for i := 0; i < fields.Len(); i++ {
		f := fields.Get(i)
		name := string(f.JSONName())
		if name == "" {
			name = string(f.Name())
		}

		opts := f.Options()
		if opts == nil || !proto.HasExtension(opts, mcppb.E_McpField) {
			continue
		}
		ext := proto.GetExtension(opts, mcppb.E_McpField).(*mcppb.MCPFieldOptions)
		if ext == nil {
			continue
		}

		result[name] = &fieldConfig{
			Description:  ext.GetDescription(),
			DefaultValue: ext.GetDefaultValue(),
			Hidden:       ext.GetHidden(),
		}
	}
	return result
}

// applyDefaults applies default values for missing arguments.
func applyDefaults(args map[string]any, fieldOpts map[string]*fieldConfig) {
	for name, cfg := range fieldOpts {
		if cfg.DefaultValue == "" {
			continue
		}
		if _, exists := args[name]; !exists {
			var val any
			if err := json.Unmarshal([]byte(cfg.DefaultValue), &val); err == nil {
				args[name] = val
			}
		}
	}
}

func (m *Middleware) callUnary(ctx context.Context, svc, method string, req proto.Message) (*mcp.CallToolResult, error) {
	endpoint := fmt.Sprintf("%s/%s/%s", m.baseURL, svc, method)

	body, err := protojson.Marshal(req)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("marshal error: %v", err)), nil
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(string(body)))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("request error: %v", err)), nil
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := m.httpClient.Do(httpReq)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("http error: %v", err)), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return mcp.NewToolResultError(fmt.Sprintf("status %d", resp.StatusCode)), nil
	}

	var result json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("decode error: %v", err)), nil
	}

	return mcp.NewToolResultText(string(result)), nil
}

func (m *Middleware) callStreaming(ctx context.Context, svc, method string, req proto.Message, outputDesc protoreflect.MessageDescriptor) (*mcp.CallToolResult, error) {
	endpoint := fmt.Sprintf("%s/%s/%s", m.baseURL, svc, method)

	body, err := protojson.Marshal(req)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("marshal error: %v", err)), nil
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(string(body)))
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("request error: %v", err)), nil
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Connect-Protocol-Version", "1")

	resp, err := m.httpClient.Do(httpReq)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("http error: %v", err)), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return mcp.NewToolResultError(fmt.Sprintf("status %d", resp.StatusCode)), nil
	}

	// Stream events via notifications, return summary at end
	return m.streamEvents(ctx, resp, svc, method)
}

// streamEvents sends each streaming event as a notification (raw JSON passthrough).
func (m *Middleware) streamEvents(ctx context.Context, resp *http.Response, svc, method string) (*mcp.CallToolResult, error) {
	var lastLine string

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		lastLine = line

		// Parse JSON and send as notification
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}

		_ = m.mcpServer.SendNotificationToClient(ctx, "notifications/message", map[string]any{
			"level":  "info",
			"logger": fmt.Sprintf("%s.%s", svc, method),
			"data":   event,
		})
	}

	// Return the last event
	if lastLine != "" {
		var lastEvent map[string]any
		if err := json.Unmarshal([]byte(lastLine), &lastEvent); err == nil {
			return mcp.NewToolResultStructured(lastEvent, "stream completed"), nil
		}
	}
	return mcp.NewToolResultText("stream completed"), nil
}

func mcpEnabled(m protoreflect.MethodDescriptor) bool {
	opts := m.Options()
	if opts == nil || !proto.HasExtension(opts, mcppb.E_Mcp) {
		return false
	}
	ext := proto.GetExtension(opts, mcppb.E_Mcp).(*mcppb.MCPOptions)
	return ext != nil && ext.GetEnabled()
}

func extractDesc(m protoreflect.MethodDescriptor, mcpOpts *mcppb.MCPOptions) string {
	// Use description from annotation if provided
	if mcpOpts.GetDescription() != "" {
		return mcpOpts.GetDescription()
	}
	// Fall back to proto comments
	if loc := m.ParentFile().SourceLocations().ByDescriptor(m); loc.LeadingComments != "" {
		return strings.TrimSpace(loc.LeadingComments)
	}
	return fmt.Sprintf("%s.%s", m.Parent().Name(), m.Name())
}

func buildSchema(desc protoreflect.MessageDescriptor, fieldOpts map[string]*fieldConfig) mcp.ToolInputSchema {
	s := mcp.ToolInputSchema{Type: "object", Properties: make(map[string]any), Required: []string{}}
	for i := 0; i < desc.Fields().Len(); i++ {
		f := desc.Fields().Get(i)
		name := string(f.JSONName())
		if name == "" {
			name = string(f.Name())
		}
		// Skip hidden arguments
		if cfg, ok := fieldOpts[name]; ok && cfg.Hidden {
			continue
		}
		s.Properties[name] = fieldSchema(f, fieldOpts)
	}
	return s
}

func fieldSchema(f protoreflect.FieldDescriptor, fieldOpts map[string]*fieldConfig) map[string]any {
	s := typeSchema(f, fieldOpts)

	// Add description from field options
	name := string(f.JSONName())
	if name == "" {
		name = string(f.Name())
	}
	if cfg, ok := fieldOpts[name]; ok && cfg.Description != "" {
		s["description"] = cfg.Description
	}

	if f.IsList() {
		return map[string]any{"type": "array", "items": s}
	}
	if f.IsMap() {
		return map[string]any{"type": "object", "additionalProperties": s}
	}
	return s
}

func typeSchema(f protoreflect.FieldDescriptor, fieldOpts map[string]*fieldConfig) map[string]any {
	s := make(map[string]any)
	switch f.Kind() {
	case protoreflect.BoolKind:
		s["type"] = "boolean"
	case protoreflect.Int32Kind, protoreflect.Int64Kind, protoreflect.Sint32Kind, protoreflect.Sint64Kind, protoreflect.Sfixed32Kind, protoreflect.Sfixed64Kind:
		s["type"] = "integer"
	case protoreflect.Uint32Kind, protoreflect.Uint64Kind, protoreflect.Fixed32Kind, protoreflect.Fixed64Kind:
		s["type"] = "integer"
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		s["type"] = "number"
	case protoreflect.StringKind:
		s["type"] = "string"
	case protoreflect.BytesKind:
		s["type"] = "string"
	case protoreflect.EnumKind:
		s["type"] = "string"
		var vals []string
		for i := 0; i < f.Enum().Values().Len(); i++ {
			vals = append(vals, string(f.Enum().Values().Get(i).Name()))
		}
		s["enum"] = vals
	case protoreflect.MessageKind:
		// For nested messages, collect their field options too
		nestedOpts := collectFieldOptions(f.Message())
		nested := buildSchema(f.Message(), nestedOpts)
		s["type"] = "object"
		s["properties"] = nested.Properties
	default:
		s["type"] = "string"
	}
	return s
}

func argsToProto(args map[string]any, msg proto.Message) error {
	data, err := json.Marshal(args)
	if err != nil {
		return err
	}
	return protojson.Unmarshal(data, msg)
}
