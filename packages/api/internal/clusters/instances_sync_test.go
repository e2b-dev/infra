package clusters

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/servicediscovery"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

// storeWithInstances builds a sync store whose creation step is a stub, so the
// keying can be exercised without a gRPC server behind every instance.
func storeWithInstances(t *testing.T) (instancesSyncStore, *smap.Map[*Instance]) {
	t.Helper()

	instances := smap.New[*Instance]()

	return instancesSyncStore{
		clusterID: uuid.New(),
		instances: instances,
		instanceCreation: func(_ context.Context, item servicediscovery.Instance) (*Instance, error) {
			return &Instance{workloadID: item.WorkloadID, NodeID: item.NodeID, LocalIPAddress: item.IPAddress}, nil
		},
	}, instances
}

// The pool's map key and its source-existence check must be the same identity.
// While the map keyed on the machine and the check keyed on the process, a
// builder that restarted on a machine already in the pool was skipped by
// PoolExists and only re-added a cycle later, after PoolRemove had taken the
// dead one out.
func TestInstancesSyncStore_ReplacesAProcessThatRestartedOnTheSameMachine(t *testing.T) {
	t.Parallel()

	store, instances := storeWithInstances(t)

	before := servicediscovery.Instance{WorkloadID: "alloc-1", NodeID: "node-a", IPAddress: "10.0.0.1"}
	store.PoolInsert(t.Context(), before)
	require.Equal(t, 1, instances.Count())

	// Same machine, new run of the process.
	after := servicediscovery.Instance{WorkloadID: "alloc-2", NodeID: "node-a", IPAddress: "10.0.0.1"}

	assert.False(t, store.PoolExists(t.Context(), after),
		"the restarted process must not read as already present just because its machine is")

	store.PoolInsert(t.Context(), after)

	assert.False(t, store.SourceExists(t.Context(), []servicediscovery.Instance{after}, mustGet(t, instances, "alloc-1")),
		"the dead run must read as gone from the source")
	assert.True(t, store.SourceExists(t.Context(), []servicediscovery.Instance{after}, mustGet(t, instances, "alloc-2")))
}

// Two builders on one machine are two pool entries; keying the map on the
// machine silently kept only the first.
func TestInstancesSyncStore_KeepsBothInstancesSharingAMachine(t *testing.T) {
	t.Parallel()

	store, instances := storeWithInstances(t)

	for _, i := range []servicediscovery.Instance{
		{WorkloadID: "alloc-1", NodeID: "node-a", IPAddress: "10.0.0.1"},
		{WorkloadID: "alloc-2", NodeID: "node-a", IPAddress: "10.0.0.2"},
	} {
		require.False(t, store.PoolExists(t.Context(), i))
		store.PoolInsert(t.Context(), i)
	}

	assert.Equal(t, 2, instances.Count())
}

// The projection newInstance performs, exercised directly: the sync tests reach
// it through a stub, so a wrong facet here survives all of them.
func TestInstanceFrom_ProjectsBothFacetsOntoThePoolEntry(t *testing.T) {
	t.Parallel()

	clusterID := uuid.New()
	instance := instanceFrom(clusterID, servicediscovery.Instance{
		WorkloadID: "alloc-1", NodeID: "node-a", IPAddress: "10.0.0.1", Port: 5008,
	}, nil)

	assert.Equal(t, "alloc-1", instance.workloadID, "the pool entry is identified by the process")
	assert.Equal(t, "node-a", instance.NodeID, "and still records the machine, which builds persist")
	assert.Equal(t, "10.0.0.1", instance.LocalIPAddress)
	assert.Equal(t, clusterID, instance.ClusterID)
}

// The remote proxy routes on the service id, which is the discovered ID. Given
// the machine instead, a call lands on whatever is running there now.
func TestRemoteInstanceAuthorization_RoutesOnTheProcess(t *testing.T) {
	t.Parallel()

	auth := remoteInstanceAuthorization("shh", true, servicediscovery.Instance{WorkloadID: "svc-aaa", NodeID: "remote-node-1"})

	assert.Equal(t, "svc-aaa", auth.serviceInstanceID)
	assert.Equal(t, "shh", auth.secret)
	assert.True(t, auth.tls)
}

func mustGet(t *testing.T, instances *smap.Map[*Instance], key string) *Instance {
	t.Helper()

	instance, found := instances.Get(key)
	require.True(t, found, "expected an instance keyed %q", key)

	return instance
}
