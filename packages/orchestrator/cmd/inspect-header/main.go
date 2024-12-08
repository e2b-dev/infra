package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"unsafe"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/build/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
)

func main() {
	buildId := flag.String("build", "", "build id")
	kind := flag.String("kind", "", "'memfile' or 'rootfs'")

	flag.Parse()

	template := storage.NewTemplateFiles(
		"",
		*buildId,
		"",
		"",
		false,
	)

	var storagePath string

	if *kind == "memfile" {
		storagePath = template.StorageMemfileHeaderPath()
	} else if *kind == "rootfs" {
		storagePath = template.StorageRootfsHeaderPath()
	} else {
		log.Fatalf("invalid kind: %s", *kind)
	}

	ctx := context.Background()

	obj := gcs.NewObject(ctx, gcs.TemplateBucket, storagePath)

	h, err := header.Deserialize(obj)
	if err != nil {
		log.Fatalf("failed to deserialize header: %s", err)
	}

	fmt.Printf("\nMETADATA\n")
	fmt.Printf("========\n")
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

	for i, mapping := range h.Mapping {
		fmt.Printf(
			"%-8d [%11d,%11d) = [%11d,%11d) in %s, %d B\n",
			i+1, mapping.Offset, mapping.Offset+mapping.Length,
			mapping.BuildStorageOffset, mapping.BuildStorageOffset+mapping.Length, mapping.BuildId.String(), mapping.Length,
		)
	}

	fmt.Printf("\nMAPPING SUMMARY\n")
	fmt.Printf("===============\n")

	builds := make(map[string]int64)

	for _, mapping := range h.Mapping {
		builds[mapping.BuildId.String()] += int64(mapping.Length)
	}

	for build, size := range builds {
		fmt.Printf("%s: %d blocks, %d MiB\n", build, size/h.Metadata.BlockSize, size/1024/1024)
	}
}
