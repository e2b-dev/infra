package network

import (
	"testing"

	networkmocks "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network/mocks"
	"github.com/hashicorp/consul/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSlotsHaveAppropriateNumbers(t *testing.T) {
	t.Run("random loop starts at 1", func(t *testing.T) {
		kv := networkmocks.NewMockCopyAndSet(t)
		kv.EXPECT().CAS(mock.Anything, mock.Anything).RunAndReturn(func(kVPair *api.KVPair, _ *api.WriteOptions) (bool, *api.WriteMeta, error) {
			assert.Equal(t, "node-id/1", kVPair.Key)
			return true, nil, nil
		})
		storage := StorageKV{slotsSize: 1, kv: kv, nodeID: "node-id"}
		result, err := storage.Acquire(t.Context())
		require.NoError(t, err)
		assert.Equal(t, 1, result.Idx)
	})

	t.Run("exhaustive loop starts at 1", func(t *testing.T) {
		/* This test verifies that the fallback loop correctly skips used slots and
		 * starts at 1. To do that, we have to guarantee that the first loop only
		 * searches for "node-id/1" and fails to set it, but the second loop skips node-id/1
		 * and searches for node-id/2. The magic happens in the mocked Keys function, which
		 * gets called in between the first and second loop.
		 */
		kv := networkmocks.NewMockCopyAndSet(t)
		attemptIndex := 0
		expectedKey := "node-id/1"
		storage := StorageKV{slotsSize: 1, kv: kv, nodeID: "node-id"}

		kv.EXPECT().CAS(mock.Anything, mock.Anything).
			RunAndReturn(func(kVPair *api.KVPair, _ *api.WriteOptions) (bool, *api.WriteMeta, error) {
				assert.Equal(t, expectedKey, kVPair.Key)
				if attemptIndex < attempts {
					attemptIndex++
					return false, nil, nil
				}
				return true, nil, nil
			})

		kv.EXPECT().Keys(mock.Anything, mock.Anything, mock.Anything).
			RunAndReturn(func(prefix string, separator string, q *api.QueryOptions) ([]string, *api.QueryMeta, error) {
				expectedKey = "node-id/2"
				storage.slotsSize = 2

				return []string{"node-id/1"}, nil, nil
			})
		result, err := storage.Acquire(t.Context())
		require.NoError(t, err)
		assert.Equal(t, 2, result.Idx)
	})
}
