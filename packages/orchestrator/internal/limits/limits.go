package limits

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const retries = 50
const maxDelay = 50 * time.Millisecond

var incrWithLimit = redis.NewScript(`
local key = KEYS[1]
local limit = ARGV[1]
local value = redis.call("GET", key)

if not value then
	value = 0
end

value = value + 1

if tonumber(value) > tonumber(limit) then
	return 0
end

redis.call("SET", key, value)

return 1
`)

var decrToZero = redis.NewScript(`
local key = KEYS[1]
local value = redis.call("GET", key)
if not value then
	return
end

redis.call("SET", key, tonumber(value) - 1)

return 1
`)

type RedisLimiter struct {
	hostname string
	client   redis.UniversalClient
}

func New(hostname string, client redis.UniversalClient) *RedisLimiter {
	return &RedisLimiter{client: client, hostname: hostname}
}

var ErrTimeout = errors.New("timeout")

func (l *RedisLimiter) key(key string) string {
	return l.hostname + ":" + key
}

func (l *RedisLimiter) TryAcquire(ctx context.Context, key string, limit int64) error {
	key = l.key(key)

	return retryBusy(retries, maxDelay, func() error {
		code, err := incrWithLimit.Run(ctx, l.client, []string{key}, limit).Int()
		if err != nil {
			return ErrRetry
		}

		switch code {
		case 0:
			return ErrRetry
		case 1:
			return nil
		default:
			panic(fmt.Sprintf("unexpected code: %d", code))
		}
	})
}

func (l *RedisLimiter) Release(ctx context.Context, key string) error {
	key = l.key(key)

	num, err := decrToZero.Run(ctx, l.client, []string{key}).Int()
	if err != nil {
		return err
	}

	if num != 1 {
		return fmt.Errorf("unexpected return: %d", num)
	}

	return nil
}
