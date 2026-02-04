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
)
