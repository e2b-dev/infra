// Package servicediscovery enumerates the running orchestrator and
// template-builder instances that callers route sandbox and build traffic to.
//
// Every backend reports a broken source the same way: the error reaches the
// caller, which skips the cycle and keeps its last known set. The listers —
// nomad.NewServices, nomad.NewNodePool, nomad.NewAllocations, kube.NewPods,
// dns.New, NewLocal, NewRemote and NewStatic — hit their source per call, and
// provider.New selects one from configuration; Cached wraps one in a
// background refresh and serves the last good set alongside the last refresh
// error, so a dead source is reported rather than read as an indefinitely
// stale one. ErrNotYetSynced distinguishes a cache that has never completed a
// refresh from one that read an empty source. NewMerged propagates either
// side's error for the same reason.
package servicediscovery

import (
	"context"
	"errors"
	"net"
	"strconv"

	"go.opentelemetry.io/otel"
)

// ErrNotYetSynced is what a cached Discoverer reports before its first refresh
// lands: no set has been read, which is not the same as reading an empty one.
var ErrNotYetSynced = errors.New("service discovery has not completed a refresh yet")

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/shared/pkg/servicediscovery")

// Instance is a single discovered instance.
//
// It carries two identities because consumers need different ones and a single
// key cannot serve both. ID answers "is this the same thing I saw last cycle",
// and every backend picks the narrowest identity its source exposes: an
// allocation or a pod, where a restart produces a new one, so a consumer
// tracking service instances notices the restart. NodeID answers "which
// machine is it on", and survives that restart, so a consumer tracking
// placement targets is not churned by it.
type Instance struct {
	// WorkloadID is unique within this instance's source. Backends that union
	// with each other must agree on it for the same instance — which is why the
	// two Nomad node backends both derive it from the node rather than reaching
	// for an allocation. Consumers compare it as an opaque string of no assumed
	// width.
	WorkloadID string

	// NodeID is the machine the instance runs on. Empty where the source has no
	// notion of one: DNS and STATIC resolve addresses, not schedulers, and the
	// machine only arrives once the instance answers over gRPC. Every source
	// the api uses reports one; the two that do not are consumed only where it
	// is never read.
	NodeID string

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

// NoSync gives the query adapters, which hold nothing to refresh, the
// lifecycle half of Discoverer.
type NoSync struct{}

func (NoSync) Start(context.Context) {}

func (NoSync) Stop(context.Context) {}
