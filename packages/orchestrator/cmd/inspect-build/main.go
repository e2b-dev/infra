package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/e2b-dev/infra/packages/orchestrator/cmd/internal/cmdutil"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func main() {
	build := flag.String("build", "", "build ID")
	template := flag.String("template", "", "template ID or alias (requires E2B_API_KEY)")
	storagePath := flag.String("storage", ".local-build", "storage: local path or gs://bucket")
	memfile := flag.Bool("memfile", false, "inspect memfile artifact")
	rootfs := flag.Bool("rootfs", false, "inspect rootfs artifact")
	mappings := flag.Bool("mappings", false, "show per-mapping listing (hidden by default)")
	data := flag.Bool("data", false, "inspect data blocks (default: header only)")
	start := flag.Int64("start", 0, "start block (only with -data)")
	end := flag.Int64("end", 0, "end block, 0 = all (only with -data)")

	validateAll := flag.Bool("validate-all", false, "validate both memfile and rootfs")
	validateMemfile := flag.Bool("validate-memfile", false, "validate memfile data integrity")
	validateRootfs := flag.Bool("validate-rootfs", false, "validate rootfs data integrity")
	colorMode := cmdutil.ColorFlag()

	flag.Parse()
	cmdutil.InitColor(*colorMode)

	if *template != "" && *build != "" {
		log.Fatal("specify either -build or -template, not both")
	}
	if *template != "" {
		resolvedBuild, err := cmdutil.ResolveTemplateID(*template)
		if err != nil {
			log.Fatalf("failed to resolve template: %s", err)
		}
		*build = resolvedBuild
		fmt.Printf("Resolved template %q to build %s\n", *template, *build)
	}
	if *build == "" {
		printUsage()
		os.Exit(1)
	}

	ctx := context.Background()

	provider, err := cmdutil.GetProvider(ctx, *storagePath)
	if err != nil {
		log.Fatalf("failed to create storage provider: %s", err)
	}

	if *validateAll || *validateMemfile || *validateRootfs {
		exitCode := 0

		if *validateAll || *validateMemfile {
			if err := validateArtifact(ctx, provider, *build, storage.MemfileName); err != nil {
				fmt.Printf("memfile validation FAILED: %s\n", err)
				exitCode = 1
			} else {
				fmt.Printf("memfile validation PASSED\n")
			}
		}

		if *validateAll || *validateRootfs {
			if err := validateArtifact(ctx, provider, *build, storage.RootfsName); err != nil {
				fmt.Printf("rootfs validation FAILED: %s\n", err)
				exitCode = 1
			} else {
				fmt.Printf("rootfs validation PASSED\n")
			}
		}

		os.Exit(exitCode)
	}

	if !*memfile && !*rootfs {
		*memfile = true // default to memfile
	}
	if *memfile && *rootfs {
		log.Fatal("specify either -memfile or -rootfs, not both")
	}

	var artifactName string
	if *memfile {
		artifactName = storage.MemfileName
	} else {
		artifactName = storage.RootfsName
	}

	headerPath := storage.TemplateFiles{BuildID: *build}.HeaderPath(artifactName)

	h, err := header.LoadHeader(ctx, provider, headerPath)
	if err != nil {
		log.Fatalf("failed to load header: %s", err)
	}

	printHeader(h, fmt.Sprintf("%s/%s", *storagePath, headerPath), *mappings)

	if *data {
		dataFile := artifactName
		inspectData(ctx, provider, *build, dataFile, h, *start, *end)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage: inspect-build (-build <uuid> | -template <id-or-alias>) [-storage <path>] [-memfile|-rootfs] [-mappings] [-data [-start N] [-end N]]\n")
	fmt.Fprintf(os.Stderr, "       inspect-build (-build <uuid> | -template <id-or-alias>) [-storage <path>] -validate-all|-validate-memfile|-validate-rootfs\n\n")
	fmt.Fprintf(os.Stderr, "The -template flag requires E2B_API_KEY environment variable.\n")
	fmt.Fprintf(os.Stderr, "Set E2B_DOMAIN for non-production environments.\n\n")
	fmt.Fprintf(os.Stderr, "Examples:\n")
	fmt.Fprintf(os.Stderr, "  inspect-build -build abc123                           # inspect memfile header\n")
	fmt.Fprintf(os.Stderr, "  inspect-build -build abc123 -mappings                 # include per-mapping listing\n")
	fmt.Fprintf(os.Stderr, "  inspect-build -template base -storage gs://bucket     # inspect by template alias\n")
	fmt.Fprintf(os.Stderr, "  inspect-build -template gtjfpksmxd9ct81x1f8e          # inspect by template ID\n")
	fmt.Fprintf(os.Stderr, "  inspect-build -build abc123 -rootfs                   # inspect rootfs header\n")
	fmt.Fprintf(os.Stderr, "  inspect-build -build abc123 -data                     # inspect memfile header + data\n")
	fmt.Fprintf(os.Stderr, "  inspect-build -build abc123 -rootfs -data -end 100    # inspect rootfs header + first 100 blocks\n")
	fmt.Fprintf(os.Stderr, "  inspect-build -build abc123 -storage gs://bucket      # inspect from GCS\n")
	fmt.Fprintf(os.Stderr, "  inspect-build -build abc123 -validate-all             # validate both memfile and rootfs\n")
}

func printHeader(h *header.Header, source string, showMappings bool) {
	err := header.ValidateMappings(h.Mapping, h.Metadata.Size, h.Metadata.BlockSize)
	if err != nil {
		fmt.Printf("\nWARNING: Mapping validation failed!\n%s\n\n", err)
	}

	fmt.Printf("\nMETADATA\n")
	fmt.Printf("========\n")
	fmt.Printf("Source             %s\n", source)
	fmt.Printf("Version            %d\n", h.Metadata.Version)
	fmt.Printf("Generation         %d\n", h.Metadata.Generation)
	fmt.Printf("Build ID           %s\n", h.Metadata.BuildId)
	fmt.Printf("Base build ID      %s\n", h.Metadata.BaseBuildId)
	fmt.Printf("Size (virtual)     %#x (%d MiB)\n", h.Metadata.Size, h.Metadata.Size/1024/1024)

	var diffU, diffC int64
	var diffIsCompressed bool
	seen := make(map[int64]bool)
	for _, mapping := range h.Mapping {
		if mapping.BuildId != h.Metadata.BuildId {
			continue
		}
		diffU += int64(mapping.Length)
		if mapping.FrameTable.IsCompressed() {
			diffIsCompressed = true
			offset := mapping.FrameTable.StartAt
			for _, frame := range mapping.FrameTable.Frames {
				if !seen[offset.C] {
					seen[offset.C] = true
					diffC += int64(frame.C)
				}
				offset.Add(frame)
			}
		}
	}
	if diffIsCompressed {
		fmt.Printf("Size (diff)        U=%#x (%d MiB), C=%#x (%d MiB)\n",
			diffU, diffU/1024/1024, diffC, diffC/1024/1024)
	} else if diffU > 0 {
		fmt.Printf("Size (diff)        U=%#x (%d MiB)\n", diffU, diffU/1024/1024)
	}

	fmt.Printf("Block size         %#x\n", h.Metadata.BlockSize)
	fmt.Printf("Blocks             %d\n", (h.Metadata.Size+h.Metadata.BlockSize-1)/h.Metadata.BlockSize)

	if showMappings {
		fmt.Printf("\nMAPPING (%d maps)\n", len(h.Mapping))
		fmt.Printf("=======\n")

		for _, mapping := range h.Mapping {
			fmt.Println(cmdutil.FormatMappingWithCompression(mapping, h.Metadata.BlockSize))
		}
	}

	fmt.Printf("\nMAPPING SUMMARY\n")
	fmt.Printf("===============\n")

	builds := make(map[string]int64)
	for _, mapping := range h.Mapping {
		builds[mapping.BuildId.String()] += int64(mapping.Length)
	}

	for buildID, size := range builds {
		var additionalInfo string
		switch buildID {
		case h.Metadata.BuildId.String():
			additionalInfo = " (current)"
		case h.Metadata.BaseBuildId.String():
			additionalInfo = " (parent)"
		case cmdutil.NilUUID:
			additionalInfo = " (sparse)"
		}
		fmt.Printf("%s%s: %d blocks, %d MiB (%0.2f%%)\n", buildID, additionalInfo, uint64(size)/h.Metadata.BlockSize, uint64(size)/1024/1024, float64(size)/float64(h.Metadata.Size)*100)
	}

	if len(h.BuildFiles) > 0 {
		fmt.Printf("\nBUILD INFO\n")
		fmt.Printf("==========\n")
		for buildID, info := range h.BuildFiles {
			var label string
			switch buildID.String() {
			case h.Metadata.BuildId.String():
				label = " (current)"
			case h.Metadata.BaseBuildId.String():
				label = " (parent)"
			}
			checksumStr := "(none)"
			if info.Checksum != [32]byte{} {
				checksumStr = fmt.Sprintf("%x", info.Checksum)
			}
			fmt.Printf("%s%s: size=%d (%s), checksum=%s\n", buildID, label, info.Size, formatSize(info.Size), checksumStr)
		}
	}

	cmdutil.PrintCompressionSummary(h)
}

func formatSize(size int64) string {
	switch {
	case size >= 1024*1024*1024:
		return fmt.Sprintf("%.1f GiB", float64(size)/1024/1024/1024)
	case size >= 1024*1024:
		return fmt.Sprintf("%.1f MiB", float64(size)/1024/1024)
	case size >= 1024:
		return fmt.Sprintf("%.1f KiB", float64(size)/1024)
	default:
		return fmt.Sprintf("%d B", size)
	}
}

func inspectData(ctx context.Context, provider storage.StorageProvider, buildID, dataFile string, h *header.Header, start, end int64) {
	blockSize := int64(h.Metadata.BlockSize)

	dataPath := storage.TemplateFiles{BuildID: buildID}.DataPath(dataFile)
	ff, err := provider.OpenFramedFile(ctx, dataPath)
	if err != nil {
		log.Fatalf("failed to open data: %s", err)
	}

	size, err := ff.Size(ctx)
	if err != nil {
		log.Fatalf("failed to get data size: %s", err)
	}

	maxBlock := size / blockSize
	if start > maxBlock {
		log.Fatalf("start block %d is out of bounds (maximum is %d)", start, maxBlock)
	}
	if end == 0 {
		end = maxBlock
	}
	if end > maxBlock {
		log.Fatalf("end block %d is out of bounds (maximum is %d)", end, maxBlock)
	}
	if start > end {
		log.Fatalf("start block %d is greater than end block %d", start, end)
	}

	fmt.Printf("\nDATA\n")
	fmt.Printf("====\n")
	fmt.Printf("Source             %s\n", dataPath)
	fmt.Printf("Size               %#x (%d MiB)\n", size, size/1024/1024)

	const readSize4MB = 4 * 1024 * 1024
	blocksPerChunk := max(int64(readSize4MB)/blockSize, 1)
	chunkSize := blockSize * blocksPerChunk
	buf := make([]byte, chunkSize)
	emptyCount := 0
	nonEmptyCount := 0

	fmt.Printf("\nBLOCKS\n")
	fmt.Printf("======\n")

	for chunkStart := start * blockSize; chunkStart < end*blockSize; chunkStart += chunkSize {
		readEnd := min(chunkStart+chunkSize, end*blockSize)
		readSize := readEnd - chunkStart

		_, err := ff.GetFrame(ctx, chunkStart, nil, false, buf[:readSize], readSize, nil)
		if err != nil {
			log.Fatalf("failed to read chunk at %#x: %s", chunkStart, err)
		}

		for off := int64(0); off < readSize; off += blockSize {
			absOff := chunkStart + off
			block := buf[off : off+blockSize]
			nonZeroCount := blockSize - int64(bytes.Count(block, []byte("\x00")))

			if nonZeroCount > 0 {
				nonEmptyCount++
				fmt.Printf("%-10d [%#x,%#x) %#x non-zero bytes\n", absOff/blockSize, absOff, absOff+blockSize, nonZeroCount)
			} else {
				emptyCount++
				fmt.Printf("%-10d [%#x,%#x) EMPTY\n", absOff/blockSize, absOff, absOff+blockSize)
			}
		}
	}

	fmt.Printf("\nDATA SUMMARY\n")
	fmt.Printf("============\n")
	fmt.Printf("Empty blocks: %d\n", emptyCount)
	fmt.Printf("Non-empty blocks: %d\n", nonEmptyCount)
	fmt.Printf("Total blocks inspected: %d\n", emptyCount+nonEmptyCount)
	fmt.Printf("Total size inspected: %#x (%d MiB)\n", int64(emptyCount+nonEmptyCount)*blockSize, int64(emptyCount+nonEmptyCount)*blockSize/1024/1024)
	fmt.Printf("Empty size: %#x (%d MiB)\n", int64(emptyCount)*blockSize, int64(emptyCount)*blockSize/1024/1024)
}
