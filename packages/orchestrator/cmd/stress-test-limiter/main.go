package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/limits"
	"github.com/redis/go-redis/v9"
	_ "modernc.org/sqlite"
)

func main() {
	ctx := context.Background()

	connectionString := os.Getenv("REDIS_CONNECTION_STRING")
	if connectionString == "" {
		panic("REDIS_CONNECTION_STRING is not set")
	}

	hostname, err := os.Hostname()
	if err != nil {
		panic(err)
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr: connectionString,
	})
	panicIfErr(redisClient.Ping(ctx).Err())

	l := limits.New(hostname, redisClient)

	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGKILL, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var acquires, releases, failedAcquires, failedReleases int
	ticker := time.NewTicker(time.Second * 1)

	var last int
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fmt.Printf("acquires: %d/sec failures: %d acks, %d rels\n",
				acquires-last, failedAcquires, failedReleases,
			)
			last = acquires
		default:
		}

		acquires++
		if err := l.TryAcquire(ctx, "test", 4); err != nil {
			failedAcquires++
			continue
		}

		releases++
		if err = l.Release(ctx, "test"); err != nil {
			failedReleases++
		}
	}
}

func panicIfErr(err error) {
	if err != nil {
		panic(err)
	}
}
