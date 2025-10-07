package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/limits"
	_ "modernc.org/sqlite"
)

func main() {
	ctx := context.Background()

	connectionString := os.Getenv("SQLITE_CONNECTION_STRING")
	if connectionString == "" {
		panic("SQLITE_CONNECTION_STRING is not set")
	}

	db, err := sql.Open("sqlite", connectionString)
	panicIfErr(err)

	if os.Getenv("SKIP_MIGRATION") != "true" {
		err = limits.Migrate(ctx, db)
		panicIfErr(err)
	}

	l := limits.New(db)

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
