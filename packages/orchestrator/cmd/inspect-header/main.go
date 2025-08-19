package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"unsafe"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func main() {
	buildId := flag.String("build", "", "build id")
	kind := flag.String("kind", "", "'memfile' or 'rootfs'")

	flag.Parse()

	template := storage.TemplateFiles{
		BuildID: *buildId,
	}

	var storagePath string

	switch *kind {
	case "memfile":
		storagePath = template.StorageMemfileHeaderPath()
	case "rootfs":
		storagePath = template.StorageRootfsHeaderPath()
	default:
		log.Fatalf("invalid kind: %s", *kind)
	}

	ctx := context.Background()
	storage, err := storage.GetTemplateStorageProvider(ctx, nil)
	if err != nil {
		log.Fatalf("failed to get storage provider: %s", err)
	}

	obj, err := storage.OpenObject(ctx, storagePath)
	if err != nil {
		log.Fatalf("failed to open object: %s", err)
	}

	h, err := header.Deserialize(obj)
	if err != nil {
		log.Fatalf("failed to deserialize header: %s", err)
	}

	fmt.Printf("\nMETADATA\n")
	fmt.Printf("========\n")
	fmt.Printf("Storage            %s/%s\n", storage.GetDetails(), storagePath)
	fmt.Printf("Version            %d\n", h.Metadata.Version)
	fmt.Printf("Generation         %d\n", h.Metadata.Generation)
	fmt.Printf("Build ID           %s\n", h.Metadata.BuildId)
	fmt.Printf("Base build ID      %s\n", h.Metadata.BaseBuildId)
	fmt.Printf("Size               %d B (%d MiB)\n", h.Metadata.Size, h.Metadata.Size/1024/1024)
	fmt.Printf("Block size         %d B\n", h.Metadata.BlockSize)
	fmt.Printf("Blocks             %d\n", (h.Metadata.Size+h.Metadata.BlockSize-1)/h.Metadata.BlockSize)

	totalSize := int64(unsafe.Sizeof(header.BuildMap{})) * int64(len(h.Mapping)) / 1024
	var sizeMessage string

	if totalSize == 0 {
		sizeMessage = "<1 KiB"
	} else {
		sizeMessage = fmt.Sprintf("%d KiB", totalSize)
	}

	fmt.Printf("\nMAPPING (%d maps, uses %s in storage)\n", len(h.Mapping), sizeMessage)
	fmt.Printf("=======\n")

	for _, mapping := range h.Mapping {
		fmt.Println(mapping.Format(h.Metadata.BlockSize))
	}

	fmt.Printf("\nMAPPING SUMMARY\n")
	fmt.Printf("===============\n")

	builds := make(map[string]int64)

	for _, mapping := range h.Mapping {
		builds[mapping.BuildId.String()] += int64(mapping.Length)
	}

	for build, size := range builds {
		var additionalInfo string

		if build == h.Metadata.BuildId.String() {
			additionalInfo = " (current)"
		} else if build == h.Metadata.BaseBuildId.String() {
			additionalInfo = " (base)"
		}

		fmt.Printf("%s%s: %d blocks, %d MiB (%0.2f%%)\n", build, additionalInfo, uint64(size)/h.Metadata.BlockSize, uint64(size)/1024/1024, float64(size)/float64(h.Metadata.Size)*100)
	}
}
