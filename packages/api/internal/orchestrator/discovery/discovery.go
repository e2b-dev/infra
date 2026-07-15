// Package discovery enumerates running orchestrator (Firecracker host) instances
// for the API to route sandbox calls to.
//
// The Discovery interface has several implementations:
//
//   - NewNomad: lists registrations of the orchestrator's Nomad-native
//     services via the local Nomad agent's HTTP /v1/service/<name> endpoint
//     (one query per configured name, unioned). Used by the Nomad-based
//     deploy; node-pool- and job-agnostic.
//
//   - NewNomadNodePool: the legacy implementation that lists ready Nomad
//     NODES in a node pool and assumes each runs an orchestrator on the
//     well-known port. Kept as a migration fallback for orchestrator jobs
//     whose service registrations are broken (empty Address); see
//     nomad_node_pool.go.
//
//   - NewMerged: unions a primary and a fallback Discovery, deduplicated by
//     ShortID with primary winning. Used to combine the two Nomad backends
//     during the migration.
//
//   - NewKubernetes: lists pods of the orchestrator DaemonSet via the
//     in-cluster K8s API. Used by the K8s deploy.
//
// The shape of the returned []Node mirrors what the existing
// orchestrator package was deriving from a Nomad node listing
// (NomadServiceDiscovery / *nomadapi.NodeListStub) so that callers can be
// switched without touching the rest of the orchestrator code path.
package discovery

import (
	"context"

	"go.opentelemetry.io/otel"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/api/internal/orchestrator/discovery")

// Node is a single discovered orchestrator instance.
type Node struct {
	// ShortID identifies the orchestrator at the discovery layer. It is the
	// (truncated) Nomad node ID for the Nomad backend, and the (truncated) pod
	// name for the Kubernetes backend. The orchestrator stores it on
	// nodemanager.Node.NomadNodeShortID, which is what
	// Orchestrator.GetNodeByNomadShortID linearly scans for; it is not used as
	// the key in Orchestrator.nodes (that map is keyed by
	// scopedNodeID(clusterID, instanceNodeID), where instanceNodeID comes from
	// the orchestrator's gRPC ServiceInfo response). The field name is
	// retained for legacy reasons but is provider-agnostic.
	ShortID string

	// IPAddress is the orchestrator host's IP. For Nomad it's the Nomad node
	// IP; for Kubernetes it's the pod's status.HostIP (orchestrator pods run
	// with host_network=true, so status.HostIP and status.PodIP are the same
	// and directly reachable from API pods).
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
