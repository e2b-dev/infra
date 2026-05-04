// Package discovery enumerates running orchestrator (Firecracker host) instances
// for the API to route sandbox calls to.
//
// Currently the only implementation is NomadDiscovery, which queries the local
// Nomad agent's HTTP /v1/nodes endpoint. The interface exists so additional
// backends can be plugged in without touching the orchestrator code path.
//
// The shape of the returned []Node mirrors what the orchestrator package was
// deriving from a Nomad node listing (NomadServiceDiscovery /
// *nomadapi.NodeListStub) so that callers can be switched without changing the
// rest of the orchestrator code path.
package discovery

import (
	"context"

	"go.opentelemetry.io/otel"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/api/internal/orchestrator/discovery")

// Node is a single discovered orchestrator instance.
type Node struct {
	// ShortID identifies the orchestrator at the discovery layer. It is the
	// (truncated) Nomad node ID for the Nomad backend. The orchestrator stores
	// it on nodemanager.Node.NomadNodeShortID, which is what
	// Orchestrator.GetNodeByNomadShortID linearly scans for; it is not used as
	// the key in Orchestrator.nodes (that map is keyed by
	// scopedNodeID(clusterID, instanceNodeID), where instanceNodeID comes from
	// the orchestrator's gRPC ServiceInfo response). The field name is
	// retained for legacy reasons but is provider-agnostic.
	ShortID string

	// IPAddress is the orchestrator host's IP (Nomad node IP for the Nomad
	// backend).
	IPAddress string

	// OrchestratorAddress is "<IPAddress>:<gRPC port>", precomputed for callers.
	OrchestratorAddress string
}

// Discovery enumerates currently running orchestrator instances.
type Discovery interface {
	// ListNodes returns every orchestrator the discovery backend knows about.
	// Implementations must be safe for concurrent use; the API calls this on a
	// 20s interval plus on-demand from the request path.
	ListNodes(ctx context.Context) ([]Node, error)
}
