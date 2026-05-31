package storage

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const rapidCachePrefix = "rapid-cache/"

type RapidCacheIndex interface {
	Touch(ctx context.Context, path string) error
	Admit(ctx context.Context, path string, size int64) error
	Evict(ctx context.Context, path string, size int64) error
	Candidates(ctx context.Context, before time.Time, limit int64) ([]string, error)
}

type noopRapidCacheIndex struct{}

func NoopRapidCacheIndex() RapidCacheIndex { return noopRapidCacheIndex{} }

func (noopRapidCacheIndex) Touch(context.Context, string) error        { return nil }
func (noopRapidCacheIndex) Admit(context.Context, string, int64) error { return nil }
func (noopRapidCacheIndex) Evict(context.Context, string, int64) error { return nil }
func (noopRapidCacheIndex) Candidates(context.Context, time.Time, int64) ([]string, error) {
	return nil, nil
}

type redisRapidCacheIndex struct {
	redis redis.UniversalClient
	keys  rapidCacheIndexKeys
}

type rapidCacheIndexKeys struct {
	chunks      string
	builds      string
	buildBytes  string
	buildChunks string
}

func NewRedisRapidCacheIndex(client redis.UniversalClient, bucket string) RapidCacheIndex {
	if client == nil || bucket == "" {
		return NoopRapidCacheIndex()
	}

	prefix := "rapid-cache:{" + bucket + "}"

	return &redisRapidCacheIndex{
		redis: client,
		keys: rapidCacheIndexKeys{
			chunks:      prefix + ":chunks",
			builds:      prefix + ":builds",
			buildBytes:  prefix + ":build_bytes",
			buildChunks: prefix + ":build_chunks",
		},
	}
}

func (i *redisRapidCacheIndex) Touch(ctx context.Context, path string) error {
	buildID := rapidCacheBuildID(path)
	if buildID == "" {
		return nil
	}

	now := float64(time.Now().Unix())
	pipe := i.redis.Pipeline()
	pipe.ZAdd(ctx, i.keys.chunks, redis.Z{Score: now, Member: path})
	pipe.ZAdd(ctx, i.keys.builds, redis.Z{Score: now, Member: buildID})
	_, err := pipe.Exec(ctx)

	return err
}

func (i *redisRapidCacheIndex) Admit(ctx context.Context, path string, size int64) error {
	buildID := rapidCacheBuildID(path)
	if buildID == "" {
		return nil
	}

	return rapidCacheAdmitScript.Run(ctx, i.redis, []string{
		i.keys.chunks,
		i.keys.builds,
		i.keys.buildBytes,
		i.keys.buildChunks,
	}, time.Now().Unix(), path, buildID, size).Err()
}

func (i *redisRapidCacheIndex) Evict(ctx context.Context, path string, size int64) error {
	buildID := rapidCacheBuildID(path)
	pipe := i.redis.Pipeline()
	pipe.ZRem(ctx, i.keys.chunks, path)
	if buildID != "" {
		pipe.HIncrBy(ctx, i.keys.buildBytes, buildID, -size)
		pipe.HIncrBy(ctx, i.keys.buildChunks, buildID, -1)
	}
	_, err := pipe.Exec(ctx)

	return err
}

func (i *redisRapidCacheIndex) Candidates(ctx context.Context, before time.Time, limit int64) ([]string, error) {
	return i.redis.ZRangeByScore(ctx, i.keys.chunks, &redis.ZRangeBy{
		Min:    "-inf",
		Max:    strconv.FormatInt(before.Unix(), 10),
		Offset: 0,
		Count:  limit,
	}).Result()
}

func rapidCacheBuildID(path string) string {
	path = strings.TrimPrefix(path, rapidCachePrefix)
	buildID, _ := SplitPath(path)

	return buildID
}

var rapidCacheAdmitScript = redis.NewScript(`
local added = redis.call('ZADD', KEYS[1], 'NX', ARGV[1], ARGV[2])
redis.call('ZADD', KEYS[1], ARGV[1], ARGV[2])
redis.call('ZADD', KEYS[2], ARGV[1], ARGV[3])
if added == 1 then
  redis.call('HINCRBY', KEYS[3], ARGV[3], ARGV[4])
  redis.call('HINCRBY', KEYS[4], ARGV[3], 1)
end
return added
`)
