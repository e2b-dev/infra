package redis

import "github.com/redis/go-redis/v9"

// Lua scripts for atomic operations.
// These scripts ensure true atomicity in Redis cluster mode
var (
	// addSandboxScript atomically stores a sandbox and adds it to the team index.
	// KEYS[1] = sandbox key, KEYS[2] = team index key
	// ARGV[1] = serialized sandbox data, ARGV[2] = sandbox ID
	addSandboxScript = redis.NewScript(`
		redis.call('SET', KEYS[1], ARGV[1])
		redis.call('SADD', KEYS[2], ARGV[2])
		return 1
	`)

	// removeSandboxScript atomically removes a sandbox and its team index entry.
	// KEYS[1] = sandbox key, KEYS[2] = team index key
	// ARGV[1] = sandbox ID
	removeSandboxScript = redis.NewScript(`
		redis.call('DEL', KEYS[1])
		redis.call('SREM', KEYS[2], ARGV[1])
		return 1
	`)

	// startTransitionScript atomically updates sandbox and sets transition key with UUID.
	// This is called AFTER Go code has validated the transition and prepared the new sandbox data.
	// KEYS[1] = sandbox key
	// KEYS[2] = transition key
	// KEYS[3] = transition result key
	// KEYS[4] = transition trace key (carries primary's W3C traceparent)
	// ARGV[1] = new sandbox JSON data
	// ARGV[2] = transition ID (UUID)
	// ARGV[3] = transition key TTL in seconds (shared with trace key)
	// ARGV[4] = result key TTL in seconds
	// ARGV[5] = primary W3C traceparent ("" to skip the trace key write)
	startTransitionScript = redis.NewScript(`
		redis.call('SET', KEYS[1], ARGV[1])
		redis.call('SET', KEYS[2], ARGV[2], 'EX', ARGV[3])
		redis.call('SET', KEYS[3], '', 'EX', ARGV[4])
		-- Refresh the trace key alongside the transition so waiters on other
		-- API pods can link their span to the primary's creation trace.
		-- SET replaces any stale value from a previously crashed primary;
		-- when the caller has no span we DEL so waiters don't read a stale link.
		if ARGV[5] ~= '' then
			redis.call('SET', KEYS[4], ARGV[5], 'EX', ARGV[3])
		else
			redis.call('DEL', KEYS[4])
		end
		return 1
	`)
)
