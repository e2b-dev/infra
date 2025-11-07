package utils

import (
	"encoding/json"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/db/types"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

// APIToOrchestratorFirewall converts API firewall config to orchestrator firewall config using JSON serialization
func APIToOrchestratorFirewall(apiFirewall *api.SandboxFirewallConfig) *orchestrator.SandboxFirewallConfig {
	if apiFirewall == nil {
		return nil
	}

	// Use JSON serialization for clean conversion
	data, err := json.Marshal(apiFirewall)
	if err != nil {
		return nil
	}

	var orchFirewall orchestrator.SandboxFirewallConfig
	if err := json.Unmarshal(data, &orchFirewall); err != nil {
		return nil
	}

	return &orchFirewall
}

// DBToAPIFirewall converts database firewall config to API firewall config using JSON serialization
func DBToAPIFirewall(dbFirewall *types.SandboxFirewallConfig) *api.SandboxFirewallConfig {
	if dbFirewall == nil {
		return nil
	}

	// Use JSON serialization for clean conversion
	data, err := json.Marshal(dbFirewall)
	if err != nil {
		return nil
	}

	var apiFirewall api.SandboxFirewallConfig
	if err := json.Unmarshal(data, &apiFirewall); err != nil {
		return nil
	}

	return &apiFirewall
}

// OrchestratorToDBFirewall converts orchestrator firewall config to database firewall config using JSON serialization
func OrchestratorToDBFirewall(orchFirewall *orchestrator.SandboxFirewallConfig) *types.SandboxFirewallConfig {
	if orchFirewall == nil {
		return nil
	}

	// Use JSON serialization for clean conversion
	data, err := json.Marshal(orchFirewall)
	if err != nil {
		return nil
	}

	var dbFirewall types.SandboxFirewallConfig
	if err := json.Unmarshal(data, &dbFirewall); err != nil {
		return nil
	}

	return &dbFirewall
}
