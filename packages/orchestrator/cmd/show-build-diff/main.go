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

	baseTemplate := storage.TemplateFiles{
		BuildID: *fromBuild,
	}

	diffTemplate := storage.TemplateFiles{
		BuildID: *toBuild,
	}

	var baseHeaderFile string
	var diffHeaderFile string

	if *memfile {
		baseHeaderFile = baseTemplate.StorageMemfileHeaderPath()
		diffHeaderFile = diffTemplate.StorageMemfileHeaderPath()
	} else {
		baseHeaderFile = baseTemplate.StorageRootfsHeaderPath()
		diffHeaderFile = diffTemplate.StorageRootfsHeaderPath()
	}

	ctx := context.Background()

	// Read headers directly
	baseData, baseSource, err := cmdutil.ReadHeader(ctx, *storagePath, baseHeaderFile)
	if err != nil {
		log.Fatalf("failed to read base header: %s", err)
	}

	diffData, diffSource, err := cmdutil.ReadHeader(ctx, *storagePath, diffHeaderFile)
	if err != nil {
		log.Fatalf("failed to read diff header: %s", err)
	}

	baseHeader, err := header.Deserialize(baseData)
	if err != nil {
		log.Fatalf("failed to deserialize base header: %s", err)
	}

	diffHeader, err := header.Deserialize(diffData)
	if err != nil {
		log.Fatalf("failed to deserialize diff header: %s", err)
	}

	fmt.Printf("\nBASE METADATA\n")
	fmt.Printf("Storage path       %s\n", baseSource)
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
	fmt.Printf("Storage path       %s\n", diffSource)
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
