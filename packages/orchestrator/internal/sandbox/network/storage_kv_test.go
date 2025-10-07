package network

import (
	"testing"

	"github.com/hashicorp/consul/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	networkmocks "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network/mocks"
)

func TestSlotsHaveAppropriateNumbers(t *testing.T) {
	kv := networkmocks.NewMockCopyAndSet(t)
	kv.EXPECT().CAS(mock.Anything, mock.Anything).RunAndReturn(func(kVPair *api.KVPair, _ *api.WriteOptions) (bool, *api.WriteMeta, error) {
		assert.Equal(t, "node-id/1", kVPair.Key)
		return true, nil, nil
	})
	storage := StorageKV{slotsSize: 1, kv: kv, nodeID: "node-id"}
	result, err := storage.Acquire(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 1, result.Idx)
}
