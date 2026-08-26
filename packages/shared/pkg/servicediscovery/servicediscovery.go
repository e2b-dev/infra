// Package servicediscovery enumerates the running orchestrator and
// template-builder instances that callers route sandbox and build traffic to.
//
// Two adapter families implement Discoverer and they report a broken source
// differently. Query adapters (NewNomad, NewNomadNodePool, NewKubernetes,
// NewLocal) hit their source on every call and return its error, so the caller
// skips the cycle and keeps its last known set; NewMerged propagates either
// side's error for the same reason. Cached adapters (NewNodePlaneInstance,
// NewK8sServiceDiscovery) serve a set that a background loop refreshes, and a
// failed refresh is logged without clearing entries, so a dead source reads as
// an indefinitely stale one and ListInstances still returns a nil error.
// NewDnsServiceDiscovery is the exception among the cached adapters: its
// refresh reconciles against the response even when every DNS exchange failed,
// so a total failure empties the set instead of preserving it. Which of these
// behaviours this layer should have is an open decision, not an oversight.
package servicediscovery

import (
	"context"
	"net"
	"strconv"

	"go.opentelemetry.io/otel"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/shared/pkg/servicediscovery")

// Instance is a single discovered instance.
type Instance struct {
	// WorkloadID identifies the scheduled unit the instance runs as, and is
	// unique within its source: the truncated Nomad node ID for the Nomad
	// backends, the pod name for the Kubernetes one, "<ip>:<port>" for the
	// address-based ones, which carry no other identity. Consumers compare it
	// as an opaque string of no assumed width.
	WorkloadID string

	// IPAddress is the host the instance's gRPC server listens on: the Nomad
	// node IP, or the pod IP of a host-networked pod.
	IPAddress string

	// Port is that server's port.
	Port uint16
}

// Address is the "<IPAddress>:<Port>" dial target.
func (i Instance) Address() string {
	return net.JoinHostPort(i.IPAddress, strconv.Itoa(int(i.Port)))
}

// Discoverer enumerates currently running instances. Implementations are safe
// for concurrent use; callers list on a fixed interval plus on demand from the
// request path.
type Discoverer interface {
	ListInstances(ctx context.Context) ([]Instance, error)

	// Start begins the background refresh a cached adapter needs before its
	// first ListInstances returns anything; query adapters ignore it.
	Start(ctx context.Context)

	// Stop ends what Start began.
	Stop(ctx context.Context)
}

// noSync gives the query adapters, which hold nothing to refresh, the
// lifecycle half of Discoverer.
type noSync struct{}

func (noSync) Start(context.Context) {}

func (noSync) Stop(context.Context) {}
