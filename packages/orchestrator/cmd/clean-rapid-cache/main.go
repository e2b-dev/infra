package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"cloud.google.com/go/storage"
	"cloud.google.com/go/storage/experimental"
	"google.golang.org/api/iterator"
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
	if err := clean(ctx, bucket, prefix, time.Now().Add(-maxAge), maxDeletions, dryRun); err != nil {
		log.Fatal(err)
	}
}

func clean(ctx context.Context, bucket string, prefix string, cutoff time.Time, maxDeletions int, dryRun bool) error {
	client, err := storage.NewGRPCClient(ctx, experimental.WithZonalBucketAPIs())
	if err != nil {
		return fmt.Errorf("create storage client: %w", err)
	}
	defer func() {
		_ = client.Close()
	}()

	var scanned, matched, deleted int
	objects := client.Bucket(bucket).Objects(ctx, &storage.Query{Prefix: prefix})
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
