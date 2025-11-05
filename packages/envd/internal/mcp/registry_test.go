package mcp

import (
	"testing"

	processrpc "github.com/e2b-dev/infra/packages/envd/internal/services/spec/process"
)

func TestMCPAnnotationFiltering(t *testing.T) {
	// Get the Process service descriptor
	fileDesc := processrpc.File_process_process_proto
	serviceDesc := fileDesc.Services().ByName("Process")

	if serviceDesc == nil {
		t.Fatal("Process service not found")
	}

	// Count methods with MCP enabled
	methods := serviceDesc.Methods()
	mcpEnabledCount := 0
	totalCount := methods.Len()

	for i := 0; i < methods.Len(); i++ {
		method := methods.Get(i)
		if isMCPEnabled(method) {
			mcpEnabledCount++
			t.Logf("MCP-enabled method: %s", method.Name())
		} else {
			t.Logf("MCP-disabled method: %s", method.Name())
		}
	}

	t.Logf("Total methods: %d, MCP-enabled: %d", totalCount, mcpEnabledCount)

	// Verify that only the List method is enabled (as per process.proto)
	if mcpEnabledCount != 1 {
		t.Errorf("Expected 1 MCP-enabled method, got %d", mcpEnabledCount)
	}

	// Verify that List is the enabled method
	listMethod := serviceDesc.Methods().ByName("List")
	if listMethod == nil {
		t.Fatal("List method not found")
	}

	if !isMCPEnabled(listMethod) {
		t.Error("List method should be MCP-enabled")
	}

	// Verify that Connect is NOT enabled
	connectMethod := serviceDesc.Methods().ByName("Connect")
	if connectMethod == nil {
		t.Fatal("Connect method not found")
	}

	if isMCPEnabled(connectMethod) {
		t.Error("Connect method should NOT be MCP-enabled")
	}
}
