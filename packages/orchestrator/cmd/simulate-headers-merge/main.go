package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/gcs"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/google/uuid"
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
		false,
	)

	diffTemplate := storage.NewTemplateFiles(
		"",
		*diffBuildId,
		"",
		"",
		false,
	)

	var baseStoragePath string
	var diffStoragePath string

	if *kind == "memfile" {
		baseStoragePath = baseTemplate.StorageMemfileHeaderPath()
		diffStoragePath = diffTemplate.StorageMemfileHeaderPath()
	} else if *kind == "rootfs" {
		baseStoragePath = baseTemplate.StorageRootfsHeaderPath()
		diffStoragePath = diffTemplate.StorageRootfsHeaderPath()
	} else {
		log.Fatalf("invalid kind: %s", *kind)
	}

	ctx := context.Background()

	baseObj := gcs.NewObject(ctx, gcs.GetTemplateBucket(), baseStoragePath)
	diffObj := gcs.NewObject(ctx, gcs.GetTemplateBucket(), diffStoragePath)

	baseHeader, err := header.Deserialize(baseObj)
	if err != nil {
		log.Fatalf("failed to deserialize base header: %s", err)
	}

	diffHeader, err := header.Deserialize(diffObj)
	if err != nil {
		log.Fatalf("failed to deserialize diff header: %s", err)
	}

	fmt.Printf("\nBASE METADATA\n")
	fmt.Printf("Storage path       %s/%s\n", gcs.GetTemplateBucket().BucketName(), baseStoragePath)
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
	fmt.Printf("Storage path       %s/%s\n", gcs.GetTemplateBucket().BucketName(), diffStoragePath)
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
