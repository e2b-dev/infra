package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"

	googleStorage "cloud.google.com/go/storage"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func getReferencedData(ctx context.Context, bucket *gcs.BucketHandle, headerPath string, kind string) ([]string, error) {
	obj := gcs.NewObject(ctx, bucket, headerPath)

	h, err := header.Deserialize(obj)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize header: %w", err)
	}

	builds := make(map[string]struct{})

	for _, mapping := range h.Mapping {
		builds[mapping.BuildId.String()] = struct{}{}
	}

	var dataReferences []string

	for build := range builds {
		template := storage.NewTemplateFiles(
			"",
			build,
			"",
			"",
			false,
		)
		if kind == "memfile" {
			dataReferences = append(dataReferences, template.StorageMemfilePath())
		} else if kind == "rootfs" {
			dataReferences = append(dataReferences, template.StorageRootfsPath())
		}
	}

	return dataReferences, nil
}

func copyFromBucket(ctx context.Context, from *gcs.BucketHandle, to *gcs.BucketHandle, objectPath string) (bool, error) {
	fromObject := gcs.NewObject(ctx, from, objectPath)

	fromSize, err := fromObject.Size()
	if err != nil {
		return false, fmt.Errorf("failed to get size of object: %w", err)
	}

	toObject := gcs.NewObject(ctx, to, objectPath)

	toSize, err := toObject.Size()
	if err != nil && !errors.Is(err, googleStorage.ErrObjectNotExist) {
		return false, fmt.Errorf("failed to get size of object: %w", err)
	}

	if fromSize == toSize {
		return false, nil
	}

	err = fromObject.Copy(ctx, toObject)
	if err != nil {
		return false, fmt.Errorf("failed to copy object: %w", err)
	}

	return true, nil
}

func main() {
	buildId := flag.String("build", "", "build id")
	from := flag.String("from", "", "from bucket")
	to := flag.String("to", "", "to bucket")

	flag.Parse()

	template := storage.NewTemplateFiles(
		"",
		*buildId,
		"",
		"",
		false,
	)

	ctx := context.Background()

	fromBucket := gcs.NewBucket(*from)
	toBucket := gcs.NewBucket(*to)

	var filesToCopy []string

	// Extract all files referenced by the build memfile header
	buildHeaderPath := template.StorageMemfileHeaderPath()
	dataReferences, err := getReferencedData(ctx, fromBucket, buildHeaderPath, "memfile")
	if err != nil {
		log.Fatalf("failed to get referenced data: %s", err)
	}

	filesToCopy = append(filesToCopy, buildHeaderPath)
	filesToCopy = append(filesToCopy, dataReferences...)

	// Extract all files referenced by the build rootfs header
	buildHeaderPath = template.StorageRootfsHeaderPath()
	dataReferences, err = getReferencedData(ctx, fromBucket, buildHeaderPath, "rootfs")
	if err != nil {
		log.Fatalf("failed to get referenced data: %s", err)
	}

	filesToCopy = append(filesToCopy, buildHeaderPath)
	filesToCopy = append(filesToCopy, dataReferences...)

	// Add the snapfile to the list of files to copy
	snapfilePath := template.StorageSnapfilePath()

	filesToCopy = append(filesToCopy, snapfilePath)

	for _, file := range filesToCopy {
		fmt.Printf("Copying %s", file)

		copied, err := copyFromBucket(ctx, fromBucket, toBucket, file)
		if err != nil {
			log.Fatalf("\nfailed to copy file: %s", err)
		}

		if copied {
			fmt.Printf(" done\n")
		} else {
			fmt.Printf(" skipped\n")
		}
	}
}
