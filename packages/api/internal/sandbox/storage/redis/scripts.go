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

	// startTransitionScript atomically updates sandbox and sets transition key.
	// This is called AFTER Go code has validated the transition and prepared the new sandbox data.
	// KEYS[1] = sandbox key, KEYS[2] = transition key
	// ARGV[1] = new sandbox JSON data
	// ARGV[2] = transition value JSON (e.g., '{"state":"pausing"}')
	// ARGV[3] = transition key TTL in seconds
	startTransitionScript = redis.NewScript(`
		local sandboxKey = KEYS[1]
		local transitionKey = KEYS[2]
		local newSandboxData = ARGV[1]
		local transitionValueJSON = ARGV[2]
		local ttlSeconds = tonumber(ARGV[3])

		-- Atomically update sandbox and set transition key
		redis.call('SET', sandboxKey, newSandboxData)
		redis.call('SET', transitionKey, transitionValueJSON, 'EX', ttlSeconds)

		return 1
	`)
)
