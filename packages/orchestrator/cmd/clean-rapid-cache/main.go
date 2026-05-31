package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	gcs "cloud.google.com/go/storage"
	"cloud.google.com/go/storage/experimental"
	"google.golang.org/api/iterator"

	"github.com/e2b-dev/infra/packages/shared/pkg/factories"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const defaultPrefix = "rapid-cache/"

func main() {
	var (
		prefix       string
		maxAge       time.Duration
		maxDeletions int
		dryRun       bool
	)

	flags := flag.NewFlagSet("clean-rapid-cache", flag.ExitOnError)
	flags.StringVar(&prefix, "prefix", defaultPrefix, "cache object prefix")
	flags.DurationVar(&maxAge, "max-age", 7*24*time.Hour, "delete objects older than this")
	flags.IntVar(&maxDeletions, "max-deletions", 10000, "maximum objects to delete")
	flags.BoolVar(&dryRun, "dry-run", true, "dry run")
	if err := flags.Parse(os.Args[1:]); err != nil {
		log.Fatal(err)
	}

	bucket := os.Getenv("RAPID_BUCKET_CACHE_BUCKET_NAME")
	if flags.NArg() > 0 {
		bucket = flags.Arg(0)
	}
	if bucket == "" {
		log.Fatal("missing bucket")
	}
	if prefix == "" {
		log.Fatal("missing prefix")
	}
	if maxAge <= 0 {
		log.Fatal("max-age must be positive")
	}
	if maxDeletions <= 0 {
		log.Fatal("max-deletions must be positive")
	}

	ctx := context.Background()
	index, closeIndex := newRapidIndex(ctx, bucket)
	err := clean(ctx, bucket, prefix, time.Now().Add(-maxAge), maxDeletions, dryRun, index)
	closeIndex()
	if err != nil {
		log.Fatal(err)
	}
}

func clean(ctx context.Context, bucket string, prefix string, cutoff time.Time, maxDeletions int, dryRun bool, index storage.RapidCacheIndex) error {
	client, err := gcs.NewGRPCClient(ctx, experimental.WithZonalBucketAPIs())
	if err != nil {
		return fmt.Errorf("create storage client: %w", err)
	}
	defer func() {
		_ = client.Close()
	}()

	if deleted, err := cleanFromIndex(ctx, client, bucket, cutoff, maxDeletions, dryRun, index); err != nil {
		return err
	} else if deleted > 0 {
		log.Printf("summary dry_run=%t deleted=%d source=redis", dryRun, deleted)

		return nil
	}

	return cleanFromBucket(ctx, client, bucket, prefix, cutoff, maxDeletions, dryRun)
}

func cleanFromIndex(ctx context.Context, client *gcs.Client, bucket string, cutoff time.Time, maxDeletions int, dryRun bool, index storage.RapidCacheIndex) (int, error) {
	candidates, _ := index.Candidates(ctx, cutoff, int64(maxDeletions))
	if len(candidates) == 0 {
		return 0, nil
	}

	deleted := 0
	for _, path := range candidates {
		obj := client.Bucket(bucket).Object(path)
		attrs, err := obj.Attrs(ctx)
		if errors.Is(err, gcs.ErrObjectNotExist) {
			if !dryRun {
				_ = index.Evict(ctx, path, 0)
			}

			continue
		}
		if err != nil {
			return deleted, fmt.Errorf("read cache object metadata: %w", err)
		}
		if dryRun {
			deleted++

			continue
		}
		if err := obj.Delete(ctx); err != nil {
			return deleted, fmt.Errorf("delete cache object: %w", err)
		}
		_ = index.Evict(ctx, path, attrs.Size)
		deleted++
	}

	return deleted, nil
}

func cleanFromBucket(ctx context.Context, client *gcs.Client, bucket string, prefix string, cutoff time.Time, maxDeletions int, dryRun bool) error {
	var scanned, matched, deleted int
	objects := client.Bucket(bucket).Objects(ctx, &gcs.Query{Prefix: prefix})
	for {
		attrs, err := objects.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return fmt.Errorf("list cache objects: %w", err)
		}

		scanned++
		if !attrs.Updated.Before(cutoff) {
			continue
		}
		matched++
		if deleted >= maxDeletions {
			break
		}
		if dryRun {
			deleted++

			continue
		}
		if err := client.Bucket(bucket).Object(attrs.Name).Delete(ctx); err != nil {
			return fmt.Errorf("delete cache object: %w", err)
		}
		deleted++
	}

	log.Printf("summary dry_run=%t scanned=%d matched=%d deleted=%d", dryRun, scanned, matched, deleted)

	return nil
}

func newRapidIndex(ctx context.Context, bucket string) (storage.RapidCacheIndex, func()) {
	redisClient, err := factories.NewRedisClient(ctx, factories.RedisConfig{
		RedisURL:         os.Getenv("REDIS_URL"),
		RedisClusterURL:  os.Getenv("REDIS_CLUSTER_URL"),
		RedisTLSCABase64: os.Getenv("REDIS_TLS_CA_BASE64"),
	})
	if err != nil {
		return storage.NoopRapidCacheIndex(), func() {}
	}

	return storage.NewRedisRapidCacheIndex(redisClient, bucket), func() {
		_ = factories.CloseCleanly(redisClient)
	}
}
