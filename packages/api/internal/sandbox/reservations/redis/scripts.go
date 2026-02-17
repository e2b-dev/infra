package redis

import (
	"fmt"

	"github.com/redis/go-redis/v9"
)

const (
	// Reserve result codes
	reserveResultReserved         = 0
	reserveResultAlreadyInStorage = 1
	reserveResultAlreadyPending   = 2
	reserveResultLimitExceeded    = 3
)

var (
	// reserveScript atomically checks limits and reserves a sandbox for creation.
	// The pending set is a ZSET where score = Unix timestamp of reservation.
	// Stale entries (older than staleTTL) are cleaned up before counting.
	//
	// KEYS[1] = storage index key (sandbox:storage:{teamID}:index)
	// KEYS[2] = pending zset key (sandbox:storage:{teamID}:reservations:pending)
	// KEYS[3] = result key (sandbox:storage:{teamID}:reservations:sandboxID:result)
	// ARGV[1] = sandboxID
	// ARGV[2] = limit (-1 means no limit)
	// ARGV[3] = current Unix timestamp (seconds, float)
	// ARGV[4] = stale cutoff Unix timestamp (now - staleTTL)
	//
	// Returns:
	//   0 = RESERVED (sandbox added to pending zset)
	//   1 = ALREADY_IN_STORAGE (sandbox exists in storage index)
	//   2 = ALREADY_PENDING (sandbox already in pending zset)
	//   3 = LIMIT_EXCEEDED (total count >= limit)
	reserveScript = redis.NewScript(fmt.Sprintf(`
		-- Clean up stale pending entries (score < cutoff)
		redis.call('ZREMRANGEBYSCORE', KEYS[2], '-inf', ARGV[4])

		-- Check if sandbox already exists in storage index
		if redis.call('SISMEMBER', KEYS[1], ARGV[1]) == 1 then
			return %d
		end

		-- Check if sandbox is already pending (has a score in the zset)
		if redis.call('ZSCORE', KEYS[2], ARGV[1]) then
			return %d
		end

		-- Check limit (ARGV[2] < 0 means no limit)
		local limit = tonumber(ARGV[2])
		if limit >= 0 then
			local storageCount = redis.call('SCARD', KEYS[1])
			local pendingCount = redis.call('ZCARD', KEYS[2])
			if storageCount + pendingCount >= limit then
				return %d
			end
		end

		-- Delete stale result key from a previous failed attempt
		redis.call('DEL', KEYS[3])
		-- Reserve: add to pending zset with current timestamp as score
		redis.call('ZADD', KEYS[2], ARGV[3], ARGV[1])
		return %d
	`, reserveResultAlreadyInStorage, reserveResultAlreadyPending, reserveResultLimitExceeded, reserveResultReserved))

	// finishStartScript removes a sandbox from the pending zset and sets the result key.
	// KEYS[1] = pending zset key
	// KEYS[2] = result key
	// ARGV[1] = sandboxID
	// ARGV[2] = result JSON
	// ARGV[3] = TTL in seconds
	finishStartScript = redis.NewScript(`
		redis.call('ZREM', KEYS[1], ARGV[1])
		redis.call('SET', KEYS[2], ARGV[2], 'EX', tonumber(ARGV[3]))
		return 1
	`)

	// releaseScript removes a sandbox from the pending zset and deletes the result key.
	// KEYS[1] = pending zset key
	// KEYS[2] = result key
	// ARGV[1] = sandboxID
	releaseScript = redis.NewScript(`
		redis.call('ZREM', KEYS[1], ARGV[1])
		redis.call('DEL', KEYS[2])
		return 1
	`)
)
