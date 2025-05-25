package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func main() {
	baseBuildId := flag.String("base", "", "base build id")
	diffBuildId := flag.String("diff", "", "diff build id")
	kind := flag.String("kind", "", "'memfile' or 'rootfs'")
	visualize := flag.Bool("visualize", false, "visualize the headers")

	flag.Parse()

	baseTemplate := storage.NewTemplateFiles(
		"",
		*baseBuildId,
		"",
		"",
	)

	diffTemplate := storage.NewTemplateFiles(
		"",
		*diffBuildId,
		"",
		"",
	)

	var baseStoragePath string
	var diffStoragePath string

	switch *kind {
	case "memfile":
		baseStoragePath = baseTemplate.StorageMemfileHeaderPath()
		diffStoragePath = diffTemplate.StorageMemfileHeaderPath()
	case "rootfs":
		baseStoragePath = baseTemplate.StorageRootfsHeaderPath()
		diffStoragePath = diffTemplate.StorageRootfsHeaderPath()
	default:
		log.Fatalf("invalid kind: %s", *kind)
	}

	ctx := context.Background()

	storage, err := storage.GetTemplateStorageProvider(ctx)
	if err != nil {
		log.Fatalf("failed to get storage provider: %s", err)
	}

	baseObj, err := storage.OpenObject(ctx, baseStoragePath)
	if err != nil {
		log.Fatalf("failed to open object: %s", err)
	}

	diffObj, err := storage.OpenObject(ctx, diffStoragePath)
	if err != nil {
		log.Fatalf("failed to open object: %s", err)
	}

	baseHeader, err := header.Deserialize(baseObj)
	if err != nil {
		log.Fatalf("failed to deserialize base header: %s", err)
	}

	diffHeader, err := header.Deserialize(diffObj)
	if err != nil {
		log.Fatalf("failed to deserialize diff header: %s", err)
	}

	fmt.Printf("\nBASE METADATA\n")
	fmt.Printf("Storage path       %s/%s\n", storage.GetDetails(), baseStoragePath)
	fmt.Printf("========\n")

	for _, mapping := range baseHeader.Mapping {
		fmt.Println(mapping.Format(baseHeader.Metadata.BlockSize))
	}

	if *visualize {
		bottomLayers := header.Layers(baseHeader.Mapping)
		delete(*bottomLayers, baseHeader.Metadata.BaseBuildId)

		fmt.Println("")
		fmt.Println(
			header.Visualize(
				baseHeader.Mapping,
				baseHeader.Metadata.Size,
				baseHeader.Metadata.BlockSize,
				128,
				bottomLayers,
				&map[uuid.UUID]struct{}{
					baseHeader.Metadata.BuildId: {},
				},
			),
		)
	}

	if err := header.ValidateMappings(baseHeader.Mapping, baseHeader.Metadata.Size, baseHeader.Metadata.BlockSize); err != nil {
		log.Fatalf("failed to validate base header: %s", err)
	}

	fmt.Printf("\nDIFF METADATA\n")
	fmt.Printf("Storage path       %s/%s\n", storage.GetDetails(), diffStoragePath)
	fmt.Printf("========\n")

	onlyDiffMappings := make([]*header.BuildMap, 0)

	for _, mapping := range diffHeader.Mapping {
		if mapping.BuildId == diffHeader.Metadata.BuildId {
			onlyDiffMappings = append(onlyDiffMappings, mapping)
		}
	}

	for _, mapping := range onlyDiffMappings {
		fmt.Println(mapping.Format(baseHeader.Metadata.BlockSize))
	}

	if *visualize {
		fmt.Println("")
		fmt.Println(
			header.Visualize(
				onlyDiffMappings,
				baseHeader.Metadata.Size,
				baseHeader.Metadata.BlockSize,
				128,
				nil,
				header.Layers(onlyDiffMappings),
			),
		)
	}

	mergedHeader := header.MergeMappings(baseHeader.Mapping, onlyDiffMappings)

	fmt.Printf("\n\nMERGED METADATA\n")
	fmt.Printf("========\n")

	for _, mapping := range mergedHeader {
		fmt.Println(mapping.Format(baseHeader.Metadata.BlockSize))
	}

	if *visualize {
		bottomLayers := header.Layers(baseHeader.Mapping)
		delete(*bottomLayers, baseHeader.Metadata.BaseBuildId)

		fmt.Println("")
		fmt.Println(
			header.Visualize(
				mergedHeader,
				baseHeader.Metadata.Size,
				baseHeader.Metadata.BlockSize,
				128,
				bottomLayers,
				header.Layers(onlyDiffMappings),
			),
		)
	}

	if err := header.ValidateMappings(mergedHeader, baseHeader.Metadata.Size, baseHeader.Metadata.BlockSize); err != nil {
		fmt.Fprintf(os.Stderr, "\n\n[VALIDATION ERROR]: failed to validate merged header: %s", err)
	}
}
