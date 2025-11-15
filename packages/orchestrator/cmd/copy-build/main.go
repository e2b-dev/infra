package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sort"
	"sync/atomic"

	googleStorage "cloud.google.com/go/storage"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func getReferencedData(ctx context.Context, bucket *storage.GCPBucketStorageProvider, headerPath string, objectType storage.ObjectType) ([]string, error) {
	obj, err := bucket.OpenObject(ctx, headerPath, objectType)
	if err != nil {
		return nil, fmt.Errorf("failed to open object: %w", err)
	}

	h, err := header.Deserialize(ctx, obj)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize header: %w", err)
	}

	builds := make(map[string]struct{})

	for _, mapping := range h.Mapping {
		builds[mapping.BuildId.String()] = struct{}{}
	}

	delete(builds, uuid.Nil.String())

	var dataReferences []string

	for build := range builds {
		template := storage.TemplateFiles{
			BuildID:            build,
			KernelVersion:      "",
			FirecrackerVersion: "",
		}

		switch objectType {
		case storage.MemfileHeaderObjectType:
			dataReferences = append(dataReferences, template.StorageMemfilePath())
		case storage.RootFSHeaderObjectType:
			dataReferences = append(dataReferences, template.StorageRootfsPath())
		}
	}

	return dataReferences, nil
}

func copyFromBucket(ctx context.Context, from *googleStorage.ObjectHandle, to *googleStorage.ObjectHandle) (bool, error) {
	fromAttrs, err := from.Attrs(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to check if the object exists: %w", err)
	}

	var toCrc uint32
	if attrs, err := to.Attrs(ctx); err == nil {
		toCrc = attrs.CRC32C
	} else if !errors.Is(err, googleStorage.ErrObjectNotExist) {
		return false, fmt.Errorf("failed to get object attributes: %w", err)
	}

	if fromAttrs.CRC32C == toCrc && fromAttrs.CRC32C != 0 {
		return false, nil
	}

	err = gcloudCopy(ctx, from, to)
	if err != nil {
		return false, fmt.Errorf("failed to copy object: %w", err)
	}

	return true, nil
}

func gcloudCopy(ctx context.Context, from, to *googleStorage.ObjectHandle) error {
	fromPath := fmt.Sprintf("gs://%s/%s", from.BucketName(), from.ObjectName())
	toPath := fmt.Sprintf("gs://%s/%s", to.BucketName(), to.ObjectName())

	cmd := exec.CommandContext(
		ctx,
		"gcloud",
		"storage",
		"cp",
		"--verbosity",
		"error",
		fromPath,
		toPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to copy GCS object: %w\n%s", err, string(output))
	}

	return nil
}

func main() {
	buildId := flag.String("build", "", "build id")
	from := flag.String("from", "", "from bucket")
	to := flag.String("to", "", "to bucket")

	flag.Parse()

	fmt.Printf("Copying build '%s' from bucket '%s' to bucket '%s'\n", *buildId, *from, *to)

	template := storage.TemplateFiles{
		BuildID:            *buildId,
		KernelVersion:      "",
		FirecrackerVersion: "",
	}

	ctx := context.Background()

	fromBucket, err := storage.NewGCPBucketStorageProvider(ctx, *from, nil)
	if err != nil {
		log.Fatalf("failed to create GCS bucket storage provider: %s", err)
	}

	var filesToCopy []string

	// Extract all files referenced by the build memfile header
	buildMemfileHeaderPath := template.StorageMemfileHeaderPath()
	dataReferences, err := getReferencedData(ctx, fromBucket, buildMemfileHeaderPath, storage.MemfileHeaderObjectType)
	if err != nil {
		log.Fatalf("failed to get referenced data: %s", err)
	}

	filesToCopy = append(filesToCopy, buildMemfileHeaderPath)
	filesToCopy = append(filesToCopy, dataReferences...)

	// Extract all files referenced by the build rootfs header
	buildRootfsHeaderPath := template.StorageRootfsHeaderPath()
	dataReferences, err = getReferencedData(ctx, fromBucket, buildRootfsHeaderPath, storage.RootFSHeaderObjectType)
	if err != nil {
		log.Fatalf("failed to get referenced data: %s", err)
	}

	filesToCopy = append(filesToCopy, buildRootfsHeaderPath)
	filesToCopy = append(filesToCopy, dataReferences...)

	// Add the snapfile to the list of files to copy
	snapfilePath := template.StorageSnapfilePath()
	filesToCopy = append(filesToCopy, snapfilePath)

	// sort files to copy
	sort.Strings(filesToCopy)

	googleStorageClient, err := googleStorage.NewClient(ctx)
	if err != nil {
		log.Fatalf("failed to create Google Storage client: %s", err)
	}

	fmt.Printf("Copying %d files\n", len(filesToCopy))

	var errgroup errgroup.Group

	errgroup.SetLimit(20)

	var done atomic.Int32

	for _, file := range filesToCopy {
		errgroup.Go(func() error {
			fmt.Printf("+ copying '%s'\n", file)

			fromObject := googleStorageClient.Bucket(*from).Object(file)
			toObject := googleStorageClient.Bucket(*to).Object(file)

			copied, err := copyFromBucket(ctx, fromObject, toObject)
			if err != nil {
				fmt.Fprintf(os.Stderr, "- failed to copy '%s': %s\n", file, err)

				return err
			}

			done.Add(1)

			if copied {
				fmt.Printf("-> [%d/%d] '%s' copied\n", done.Load(), len(filesToCopy), file)
			} else {
				fmt.Printf("-> [%d/%d] '%s' already exists, skipping\n", done.Load(), len(filesToCopy), file)
			}

			return nil
		})
	}

	if err := errgroup.Wait(); err != nil {
		log.Fatalf("failed to copy files: %s", err)
	}

	fmt.Printf("Build '%s' copied to bucket '%s'\n", *buildId, *to)
}
