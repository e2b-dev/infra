package plugin

import (
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad-autoscaler/plugins"
	"github.com/hashicorp/nomad-autoscaler/plugins/apm"
	"github.com/hashicorp/nomad-autoscaler/plugins/base"
	"github.com/hashicorp/nomad-autoscaler/sdk"
	"github.com/hashicorp/nomad/api"
)

const (
	// PluginName is the name of the plugin - must match binary name
	PluginName = "nomad-nodepool-apm"

	// PluginType for APM plugins
	PluginTypeAPM = "apm"
)

var (
	PluginID = plugins.PluginID{
		Name:       PluginName,
		PluginType: PluginTypeAPM,
	}

	PluginConfig = &plugins.InternalPluginConfig{
		Factory: func(l hclog.Logger) any { return NewNodePoolPlugin(l) },
	}

	pluginInfo = &base.PluginInfo{
		Name:       PluginName,
		PluginType: PluginTypeAPM,
	}

	// filterValueEscaper escapes special characters for Nomad filter expressions.
	// Nomad uses go-bexpr which requires escaping backslashes and double quotes
	// within quoted string values.
	filterValueEscaper = strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
	)
)

// NodePoolPlugin is the APM plugin implementation that queries
// the Nomad API to get the count of nodes in a specific node pool.
type NodePoolPlugin struct {
	client *api.Client
	config map[string]string
	logger hclog.Logger
}

// NewNodePoolPlugin creates a new NodePoolPlugin instance.
func NewNodePoolPlugin(log hclog.Logger) apm.APM {
	return &NodePoolPlugin{
		logger: log,
	}
}

// SetConfig satisfies the SetConfig function on the base.Base interface.
func (p *NodePoolPlugin) SetConfig(config map[string]string) error {
	p.config = config

	cfg := api.DefaultConfig()

	// Allow overriding Nomad address
	if addr, ok := config["nomad_address"]; ok && addr != "" {
		cfg.Address = addr
	}

	// Allow overriding Nomad token
	if token, ok := config["nomad_token"]; ok && token != "" {
		cfg.SecretID = token
	}

	// Allow overriding Nomad region
	if region, ok := config["nomad_region"]; ok && region != "" {
		cfg.Region = region
	}

	// Allow overriding Nomad namespace
	if namespace, ok := config["nomad_namespace"]; ok && namespace != "" {
		cfg.Namespace = namespace
	}

	client, err := api.NewClient(cfg)
	if err != nil {
		return fmt.Errorf("failed to create Nomad client: %w", err)
	}

	p.client = client
	p.logger.Info("nomad-nodepool APM plugin configured",
		"address", cfg.Address,
		"region", cfg.Region,
	)

	return nil
}

// PluginInfo satisfies the PluginInfo function on the base.Base interface.
func (p *NodePoolPlugin) PluginInfo() (*base.PluginInfo, error) {
	return pluginInfo, nil
}

// Query retrieves the node count for a given node pool.
// The query parameter should be the name of the node pool (e.g., "build").
func (p *NodePoolPlugin) Query(query string, _ sdk.TimeRange) (sdk.TimestampedMetrics, error) {
	nodePool := query

	if nodePool == "" {
		return nil, fmt.Errorf("node pool name is required as query parameter")
	}

	p.logger.Debug("querying node count for node pool", "node_pool", nodePool)

	// List nodes filtered by node pool.
	// Escape special characters in the node pool name to prevent filter injection.
	escapedNodePool := filterValueEscaper.Replace(nodePool)
	nodes, _, err := p.client.Nodes().List(&api.QueryOptions{
		Filter: fmt.Sprintf(`NodePool == "%s"`, escapedNodePool),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes in pool %q: %w", nodePool, err)
	}

	// Count only nodes that are ready and eligible for scheduling
	readyCount := 0
	for _, node := range nodes {
		if node.Status == api.NodeStatusReady && node.SchedulingEligibility == api.NodeSchedulingEligible {
			readyCount++
		}
	}

	p.logger.Info("node count query completed",
		"node_pool", nodePool,
		"total_nodes", len(nodes),
		"ready_eligible_nodes", readyCount,
	)

	return sdk.TimestampedMetrics{
		{
			Timestamp: time.Now(),
			Value:     float64(readyCount),
		},
	}, nil
}

// QueryMultiple satisfies the QueryMultiple function on the apm.APM interface.
// This is used by Dynamic Application Sizing (DAS) feature.
// For our use case, we return the same metrics in an array format.
func (p *NodePoolPlugin) QueryMultiple(query string, timeRange sdk.TimeRange) ([]sdk.TimestampedMetrics, error) {
	metrics, err := p.Query(query, timeRange)
	if err != nil {
		return nil, err
	}

	// Return as array of TimestampedMetrics
	return []sdk.TimestampedMetrics{metrics}, nil
}
