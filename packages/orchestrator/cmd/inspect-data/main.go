package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

func main() {
	buildId := flag.String("build", "", "build id")
	kind := flag.String("kind", "", "'memfile' or 'rootfs'")
	start := flag.Int64("start", 0, "start block")
	end := flag.Int64("end", 0, "end block")

	flag.Parse()

	template := storage.TemplateFiles{
		BuildID: *buildId,
	}

	var storagePath string
	var blockSize int64

	switch *kind {
	case "memfile":
		storagePath = template.StorageMemfilePath()
		blockSize = 2097152
	case "rootfs":
		storagePath = template.StorageRootfsPath()
		blockSize = 4096
	default:
		log.Fatalf("invalid kind: %s", *kind)
	}

	if *end == 0 {
		*end = blockSize
	}

	ctx := context.Background()

	storage, err := storage.GetTemplateStorageProvider(ctx, nil, block.ChunkSize)
	if err != nil {
		log.Fatalf("failed to get storage provider: %s", err)
	}

	obj, err := storage.OpenObject(ctx, storagePath)
	if err != nil {
		log.Fatalf("failed to open object: %s", err)
	}

	size, err := obj.Size()
	if err != nil {
		log.Fatalf("failed to get object size: %s", err)
	}

	if *start > size/blockSize {
		log.Fatalf("start block %d is out of bounds (maximum is %d)", *start, size/blockSize)
	}

	if *end > size/blockSize {
		log.Fatalf("end block %d is out of bounds (maximum is %d)", *end, size/blockSize)
	}

	if *start > *end {
		log.Fatalf("start block %d is greater than end block %d", *start, *end)
	}

	fmt.Printf("\nMETADATA\n")
	fmt.Printf("========\n")
	fmt.Printf("Storage            %s/%s\n", storage.GetDetails(), storagePath)
	fmt.Printf("Build ID           %s\n", *buildId)
	fmt.Printf("Size               %d B (%d MiB)\n", size, size/1024/1024)
	fmt.Printf("Block size         %d B\n", blockSize)

	b := make([]byte, blockSize)

	fmt.Printf("\nDATA\n")
	fmt.Printf("====\n")

	emptyCount := 0
	nonEmptyCount := 0

	for i := *start * blockSize; i < *end*blockSize; i += blockSize {
		_, err := obj.ReadAt(b, i)
		if err != nil {
			log.Fatalf("failed to read block: %s", err)
		}

		nonZeroCount := blockSize - int64(bytes.Count(b, []byte("\x00")))

		if nonZeroCount > 0 {
			nonEmptyCount++
			fmt.Printf("%-10d [%11d,%11d) %d non-zero bytes\n", i/blockSize, i, i+blockSize, nonZeroCount)
		} else {
			emptyCount++
			fmt.Printf("%-10d [%11d,%11d) EMPTY\n", i/blockSize, i, i+blockSize)
		}
	}

	fmt.Printf("\nSUMMARY\n")
	fmt.Printf("=======\n")
	fmt.Printf("Empty inspected blocks: %d\n", emptyCount)
	fmt.Printf("Non-empty inspected blocks: %d\n", nonEmptyCount)
	fmt.Printf("Total inspected blocks: %d\n", emptyCount+nonEmptyCount)
	fmt.Printf("Total inspected size: %d B (%d MiB)\n", int64(emptyCount+nonEmptyCount)*blockSize, int64(emptyCount+nonEmptyCount)*blockSize/1024/1024)
	fmt.Printf("Empty inspected size: %d B (%d MiB)\n", int64(emptyCount)*blockSize, int64(emptyCount)*blockSize/1024/1024)
}
