package redis

import (
	redis_utils "github.com/e2b-dev/infra/packages/shared/pkg/redis"
)

const (
	reservationKeyPrefix = "sandbox:reservation"
	storageKeyPrefix     = "sandbox:storage"
	pendingKey = "pending"
	resultKey            = "result"
	indexKey             = "index"
)

// getStorageIndexKey returns the existing storage team index key (read-only).
// This matches the key produced by storage/redis/utils.go:getTeamIndexKey.
func getStorageIndexKey(teamID string) string {
	return redis_utils.CreateKey(storageKeyPrefix, redis_utils.SameSlot(teamID), indexKey)
}

// getPendingSetKey returns the key for the pending set of sandbox IDs being created.
func getPendingSetKey(teamID string) string {
	return redis_utils.CreateKey(reservationKeyPrefix, redis_utils.SameSlot(teamID), pendingKey)
}

// getResultKey returns the key for a sandbox creation result.
func getResultKey(teamID, sandboxID string) string {
	return redis_utils.CreateKey(reservationKeyPrefix, redis_utils.SameSlot(teamID), sandboxID, resultKey)
}

