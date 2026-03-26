package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/orchestrator/cmd/internal/cmdutil"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func main() {
	fromBuild := flag.String("from-build", "", "base build ID")
	toBuild := flag.String("to-build", "", "diff build ID")
	storagePath := flag.String("storage", ".local-build", "storage: local path or gs://bucket")
	memfile := flag.Bool("memfile", false, "inspect memfile artifact")
	rootfs := flag.Bool("rootfs", false, "inspect rootfs artifact")
	visualize := flag.Bool("visualize", false, "visualize the headers")

	flag.Parse()

	if *fromBuild == "" {
		log.Fatal("-from-build required")
	}
	if *toBuild == "" {
		log.Fatal("-to-build required")
	}

	// Determine artifact type
	if !*memfile && !*rootfs {
		*memfile = true // default to memfile
	}
	if *memfile && *rootfs {
		log.Fatal("specify either -memfile or -rootfs, not both")
	}

	artifactName := storage.MemfileName
	if *rootfs {
		artifactName = storage.RootfsName
	}

	baseHeaderPath := storage.TemplateFiles{BuildID: *fromBuild}.HeaderPath(artifactName)
	diffHeaderPath := storage.TemplateFiles{BuildID: *toBuild}.HeaderPath(artifactName)

	ctx := context.Background()

	provider, err := cmdutil.GetProvider(ctx, *storagePath)
	if err != nil {
		log.Fatalf("failed to create storage provider: %s", err)
	}

	baseHeader, err := header.LoadHeader(ctx, provider, baseHeaderPath)
	if err != nil {
		log.Fatalf("failed to load base header: %s", err)
	}

	diffHeader, err := header.LoadHeader(ctx, provider, diffHeaderPath)
	if err != nil {
		log.Fatalf("failed to load diff header: %s", err)
	}

	fmt.Printf("\nBASE METADATA\n")
	fmt.Printf("Storage path       %s/%s\n", *storagePath, baseHeaderPath)
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
	fmt.Printf("Storage path       %s/%s\n", *storagePath, diffHeaderPath)
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

	mergedHeader, err := header.MergeMappings(baseHeader.Mapping, onlyDiffMappings)
	if err != nil {
		log.Fatalf("failed to merge mappings: %v", err)
	}

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
