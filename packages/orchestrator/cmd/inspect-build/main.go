package main

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"net/http"
	"os"
	"slices"
	"strings"
	"unsafe"

	"github.com/google/uuid"

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
	summary := flag.Bool("summary", false, "show only metadata + summary (skip per-mapping listing)")
	listFiles := flag.Bool("list-files", false, "list all files for this build with existence and size info")
	data := flag.Bool("data", false, "inspect data blocks (default: header only)")
	start := flag.Int64("start", 0, "start block (only with -data)")
	end := flag.Int64("end", 0, "end block, 0 = all (only with -data)")

	// Validation flags
	validateAll := flag.Bool("validate-all", false, "validate both memfile and rootfs")
	validateMemfile := flag.Bool("validate-memfile", false, "validate memfile data integrity")
	validateRootfs := flag.Bool("validate-rootfs", false, "validate rootfs data integrity")

	flag.Parse()

	// Resolve build ID from template if provided
	if *template != "" && *build != "" {
		log.Fatal("specify either -build or -template, not both")
	}
	if *template != "" {
		resolvedBuild, err := resolveTemplateID(*template)
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

	// Handle list-files mode
	if *listFiles {
		printFileList(ctx, *storagePath, *build)
		os.Exit(0)
	}

	// Handle validation mode
	if *validateAll || *validateMemfile || *validateRootfs {
		exitCode := 0

		if *validateAll || *validateMemfile {
			if err := validateArtifact(ctx, *storagePath, *build, "memfile"); err != nil {
				fmt.Printf("memfile validation FAILED: %s\n", err)
				exitCode = 1
			} else {
				fmt.Printf("memfile validation PASSED\n")
			}
		}

		if *validateAll || *validateRootfs {
			if err := validateArtifact(ctx, *storagePath, *build, storage.RootfsName); err != nil {
				fmt.Printf("rootfs validation FAILED: %s\n", err)
				exitCode = 1
			} else {
				fmt.Printf("rootfs validation PASSED\n")
			}
		}

		os.Exit(exitCode)
	}

	// Determine artifact type for inspection
	if !*memfile && !*rootfs {
		*memfile = true // default to memfile
	}
	if *memfile && *rootfs {
		log.Fatal("specify either -memfile or -rootfs, not both")
	}

	var artifactName string
	if *memfile {
		artifactName = "memfile"
	} else {
		artifactName = storage.RootfsName
	}

	// Read from unified header path (auto-detects V3/V4)
	headerFile := artifactName + storage.HeaderSuffix
	headerData, headerSource, err := cmdutil.ReadFile(ctx, *storagePath, *build, headerFile)
	if err != nil {
		log.Fatalf("failed to read header: %s", err)
	}

	h, err := header.Deserialize(headerData)
	if err != nil {
		log.Fatalf("failed to deserialize header: %s", err)
	}

	// Print header info
	printHeader(h, headerSource, *summary)

	// If -data flag, also inspect data blocks
	if *data {
		dataFile := artifactName
		inspectData(ctx, *storagePath, *build, dataFile, h, *start, *end)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage: inspect-build (-build <uuid> | -template <id-or-alias>) [-storage <path>] [-memfile|-rootfs] [-summary] [-data [-start N] [-end N]]\n")
	fmt.Fprintf(os.Stderr, "       inspect-build (-build <uuid> | -template <id-or-alias>) [-storage <path>] -validate-all|-validate-memfile|-validate-rootfs\n")
	fmt.Fprintf(os.Stderr, "       inspect-build (-build <uuid> | -template <id-or-alias>) [-storage <path>] -list-files\n\n")
	fmt.Fprintf(os.Stderr, "The -template flag requires E2B_API_KEY environment variable.\n")
	fmt.Fprintf(os.Stderr, "Set E2B_DOMAIN for non-production environments.\n\n")
	fmt.Fprintf(os.Stderr, "Examples:\n")
	fmt.Fprintf(os.Stderr, "  inspect-build -build abc123                           # inspect memfile header\n")
	fmt.Fprintf(os.Stderr, "  inspect-build -build abc123 -summary                  # metadata + summaries only\n")
	fmt.Fprintf(os.Stderr, "  inspect-build -build abc123 -list-files               # list all build files\n")
	fmt.Fprintf(os.Stderr, "  inspect-build -template base -storage gs://bucket     # inspect by template alias\n")
	fmt.Fprintf(os.Stderr, "  inspect-build -template gtjfpksmxd9ct81x1f8e          # inspect by template ID\n")
	fmt.Fprintf(os.Stderr, "  inspect-build -build abc123 -rootfs                   # inspect rootfs header\n")
	fmt.Fprintf(os.Stderr, "  inspect-build -build abc123 -data                     # inspect memfile header + data\n")
	fmt.Fprintf(os.Stderr, "  inspect-build -build abc123 -rootfs -data -end 100    # inspect rootfs header + first 100 blocks\n")
	fmt.Fprintf(os.Stderr, "  inspect-build -build abc123 -storage gs://bucket      # inspect from GCS\n")
	fmt.Fprintf(os.Stderr, "  inspect-build -build abc123 -validate-all             # validate both memfile and rootfs\n")
	fmt.Fprintf(os.Stderr, "  inspect-build -build abc123 -validate-memfile         # validate memfile integrity\n")
}

func printHeader(h *header.Header, source string, summaryOnly bool) {
	// Validate mappings
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
	fmt.Printf("Size               %#x (%d MiB)\n", h.Metadata.Size, h.Metadata.Size/1024/1024)
	fmt.Printf("Block size         %#x\n", h.Metadata.BlockSize)
	fmt.Printf("Blocks             %d\n", (h.Metadata.Size+h.Metadata.BlockSize-1)/h.Metadata.BlockSize)

	if !summaryOnly {
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

	// Print compression summary
	cmdutil.PrintCompressionSummary(h)
}

// printFileList lists all files that actually exist for this build in storage.
func printFileList(ctx context.Context, storagePath, buildID string) {
	fmt.Printf("\nFILES for build %s\n", buildID)
	fmt.Printf("====================\n")

	files, err := cmdutil.ListFiles(ctx, storagePath, buildID)
	if err != nil {
		fmt.Printf("ERROR listing files: %s\n", err)

		return
	}

	if len(files) == 0 {
		fmt.Printf("(no files found)\n")

		return
	}

	fmt.Printf("%-45s  %12s\n", "FILE", "SIZE")
	fmt.Printf("%-45s  %12s\n", strings.Repeat("-", 45), strings.Repeat("-", 12))

	for _, info := range files {
		extra := ""
		if uSize, ok := info.Metadata[storage.MetadataKeyUncompressedSize]; ok {
			extra = fmt.Sprintf("  (%s=%s)", storage.MetadataKeyUncompressedSize, uSize)
		}
		fmt.Printf("%-45s  %12s%s\n", info.Name, formatSize(info.Size), extra)
	}

	fmt.Printf("\n%d files total\n", len(files))
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

func inspectData(ctx context.Context, storagePath, buildID, dataFile string, h *header.Header, start, end int64) {
	blockSize := int64(h.Metadata.BlockSize)

	reader, size, source, err := cmdutil.OpenDataFile(ctx, storagePath, buildID, dataFile)
	if err != nil {
		log.Fatalf("failed to open data: %s", err)
	}

	// Validate bounds before defer to avoid exitAfterDefer lint error
	maxBlock := size / blockSize
	if start > maxBlock {
		reader.Close()
		log.Fatalf("start block %d is out of bounds (maximum is %d)", start, maxBlock)
	}
	if end == 0 {
		end = maxBlock
	}
	if end > maxBlock {
		reader.Close()
		log.Fatalf("end block %d is out of bounds (maximum is %d)", end, maxBlock)
	}
	if start > end {
		reader.Close()
		log.Fatalf("start block %d is greater than end block %d", start, end)
	}

	fmt.Printf("\nDATA\n")
	fmt.Printf("====\n")
	fmt.Printf("Source             %s\n", source)
	fmt.Printf("Size               %#x (%d MiB)\n", size, size/1024/1024)

	b := make([]byte, blockSize)
	emptyCount := 0
	nonEmptyCount := 0

	fmt.Printf("\nBLOCKS\n")
	fmt.Printf("======\n")

	for i := start * blockSize; i < end*blockSize; i += blockSize {
		_, err := reader.ReadAt(b, i)
		if err != nil {
			reader.Close()
			log.Fatalf("failed to read block: %s", err)
		}

		nonZeroCount := blockSize - int64(bytes.Count(b, []byte("\x00")))

		if nonZeroCount > 0 {
			nonEmptyCount++
			fmt.Printf("%-10d [%#x,%#x) %#x non-zero bytes\n", i/blockSize, i, i+blockSize, nonZeroCount)
		} else {
			emptyCount++
			fmt.Printf("%-10d [%#x,%#x) EMPTY\n", i/blockSize, i, i+blockSize)
		}
	}

	fmt.Printf("\nDATA SUMMARY\n")
	fmt.Printf("============\n")
	fmt.Printf("Empty blocks: %d\n", emptyCount)
	fmt.Printf("Non-empty blocks: %d\n", nonEmptyCount)
	fmt.Printf("Total blocks inspected: %d\n", emptyCount+nonEmptyCount)
	fmt.Printf("Total size inspected: %#x (%d MiB)\n", int64(emptyCount+nonEmptyCount)*blockSize, int64(emptyCount+nonEmptyCount)*blockSize/1024/1024)
	fmt.Printf("Empty size: %#x (%d MiB)\n", int64(emptyCount)*blockSize, int64(emptyCount)*blockSize/1024/1024)

	reader.Close()
}

// validateArtifact validates data integrity for an artifact (memfile or rootfs).
func validateArtifact(ctx context.Context, storagePath, buildID, artifactName string) error {
	fmt.Printf("\n=== Validating %s for build %s ===\n", artifactName, buildID)

	// 1. Read and deserialize header
	headerFile := artifactName + ".header"
	headerData, _, err := cmdutil.ReadFile(ctx, storagePath, buildID, headerFile)
	if err != nil {
		return fmt.Errorf("failed to read header: %w", err)
	}

	h, err := header.Deserialize(headerData)
	if err != nil {
		return fmt.Errorf("failed to deserialize header: %w", err)
	}
	fmt.Printf("  Header: version=%d size=%#x blockSize=%#x mappings=%d\n",
		h.Metadata.Version, h.Metadata.Size, h.Metadata.BlockSize, len(h.Mapping))

	// 2. Validate mappings cover entire file
	if err := header.ValidateHeader(h); err != nil {
		return fmt.Errorf("header validation failed: %w", err)
	}
	fmt.Printf("  Mappings: coverage validated\n")

	// 3. Validate FrameTable offset consistency for each mapping
	if h.Metadata.Version >= header.MetadataVersionCompressed {
		if err := validateFrameTableOffsets(h); err != nil {
			return fmt.Errorf("frame table offset validation failed: %w", err)
		}
	}

	// 4. Reconstruct and verify key virtual offsets through the header chain
	if h.Metadata.Version >= header.MetadataVersionCompressed {
		if err := validateReconstruction(ctx, storagePath, artifactName, h); err != nil {
			return fmt.Errorf("reconstruction validation failed: %w", err)
		}
	}

	// 5. Validate compressed frames if header is V4 (works with compressed-only builds)
	if h.Metadata.Version >= header.MetadataVersionCompressed {
		if err := validateCompressedFrames(ctx, storagePath, artifactName, h); err != nil {
			return fmt.Errorf("compressed frame validation failed: %w", err)
		}
	} else {
		// For uncompressed V3 headers, open data file and compute MD5
		reader, dataSize, _, err := cmdutil.OpenDataFile(ctx, storagePath, buildID, artifactName)
		if err != nil {
			return fmt.Errorf("failed to open data file: %w", err)
		}
		defer reader.Close()

		fmt.Printf("  Data file: size=%#x\n", dataSize)

		hash := md5.New()
		chunkSize := int64(1024 * 1024)
		buf := make([]byte, chunkSize)

		for offset := int64(0); offset < dataSize; offset += chunkSize {
			readSize := chunkSize
			if offset+chunkSize > dataSize {
				readSize = dataSize - offset
			}
			n, err := reader.ReadAt(buf[:readSize], offset)
			if err != nil && n == 0 {
				return fmt.Errorf("failed to read at offset %d: %w", offset, err)
			}
			hash.Write(buf[:n])
		}

		dataMD5 := hex.EncodeToString(hash.Sum(nil))
		fmt.Printf("  Data MD5 (storage): %s\n", dataMD5)
	}

	return nil
}

// readVirtualOffset reconstructs bytes at a virtual offset by following the header chain.
// Returns the bytes and the build/offset info for logging.
func readVirtualOffset(ctx context.Context, storagePath, artifactName string, h *header.Header, virtualOffset int64, length int) ([]byte, string, error) {
	mapping, err := h.GetShiftedMapping(ctx, virtualOffset)
	if err != nil {
		return nil, "", fmt.Errorf("GetShiftedMapping(%#x): %w", virtualOffset, err)
	}

	if mapping.BuildId == uuid.Nil {
		// Zero-fill
		return make([]byte, length), fmt.Sprintf("zero-fill at %#x", virtualOffset), nil
	}

	ft := mapping.FrameTable
	storageOff := int64(mapping.Offset) // This is BuildStorageOffset + shift

	if !storage.IsCompressed(ft) {
		// Uncompressed — just read directly
		reader, _, _, err := cmdutil.OpenDataFile(ctx, storagePath, mapping.BuildId.String(), artifactName)
		if err != nil {
			return nil, "", fmt.Errorf("open uncompressed %s: %w", mapping.BuildId, err)
		}
		defer reader.Close()

		buf := make([]byte, length)
		_, err = reader.ReadAt(buf, storageOff)
		if err != nil {
			return nil, "", fmt.Errorf("read uncompressed at %#x: %w", storageOff, err)
		}

		return buf, fmt.Sprintf("uncompressed build=%s off=%#x", mapping.BuildId, storageOff), nil
	}

	// Compressed — find frame, decompress, extract bytes
	frameStart, frameSize, err := ft.FrameFor(storageOff)
	if err != nil {
		return nil, "", fmt.Errorf("FrameFor(%#x): %w", storageOff, err)
	}

	compressedFile := storage.CompressedDataName(artifactName, ft.CompressionType)
	compReader, _, _, err := cmdutil.OpenDataFile(ctx, storagePath, mapping.BuildId.String(), compressedFile)
	if err != nil {
		return nil, "", fmt.Errorf("open compressed %s: %w", mapping.BuildId, err)
	}
	defer compReader.Close()

	compBuf := make([]byte, frameSize.C)
	_, err = compReader.ReadAt(compBuf, frameStart.C)
	if err != nil {
		return nil, "", fmt.Errorf("read compressed at C=%#x: %w", frameStart.C, err)
	}

	decompressed, err := storage.DecompressFrame(ft.CompressionType, compBuf, frameSize.U)
	if err != nil {
		return nil, "", fmt.Errorf("decompress frame: %w", err)
	}

	// The desired byte is at storageOff within the decompressed frame
	offsetInFrame := storageOff - frameStart.U
	if offsetInFrame < 0 || offsetInFrame+int64(length) > int64(len(decompressed)) {
		return nil, "", fmt.Errorf("offset %#x (in-frame=%#x) out of bounds (frame size=%#x)", storageOff, offsetInFrame, len(decompressed))
	}

	info := fmt.Sprintf("compressed build=%s storageOff=%#x frameU=%#x offsetInFrame=%#x", mapping.BuildId, storageOff, frameStart.U, offsetInFrame)

	return decompressed[offsetInFrame : offsetInFrame+int64(length)], info, nil
}

// validateReconstruction simulates the runtime read path by following the header
// chain for specific virtual offsets and verifying the data.
func validateReconstruction(ctx context.Context, storagePath, artifactName string, h *header.Header) error {
	fmt.Printf("  Reconstructing key virtual offsets through header chain\n")

	blockSize := int64(h.Metadata.BlockSize)

	// For rootfs: check ext4 superblock magic at offset 0x438 (byte 56 of superblock at 0x400)
	// Read from block 0 (block-aligned) and check bytes within the block
	if artifactName == storage.RootfsName && h.Metadata.Size > 0x1000 {
		readLen := min(int(blockSize), 4096)
		buf, info, err := readVirtualOffset(ctx, storagePath, artifactName, h, 0, readLen)
		if err != nil {
			return fmt.Errorf("read ext4 superblock block: %w", err)
		}

		if len(buf) > 0x439 {
			magic := uint16(buf[0x438]) | uint16(buf[0x439])<<8
			if magic != 0xEF53 {
				return fmt.Errorf("ext4 superblock magic at byte 0x438 = %#04x (expected 0xEF53) (%s)", magic, info)
			}
			fmt.Printf("    ext4 superblock at 0x438: magic=0xEF53 OK (%s)\n", info)
		}
	}

	// Check first block, a middle block, and last block
	checkOffsets := []int64{0}
	if h.Metadata.Size > uint64(blockSize*2) {
		midBlock := int64(h.Metadata.Size) / 2
		midBlock = (midBlock / blockSize) * blockSize
		checkOffsets = append(checkOffsets, midBlock)
	}
	lastBlock := int64(h.Metadata.Size) - blockSize
	if lastBlock > 0 {
		checkOffsets = append(checkOffsets, lastBlock)
	}

	for _, vOff := range checkOffsets {
		readLen := min(int(blockSize), 4096)
		buf, info, err := readVirtualOffset(ctx, storagePath, artifactName, h, vOff, readLen)
		if err != nil {
			return fmt.Errorf("read at virtual offset %#x: %w", vOff, err)
		}

		crc := crc32.ChecksumIEEE(buf)
		fmt.Printf("    vOff=%#x (%d bytes) crc32=%#08x (%s)\n", vOff, readLen, crc, info)
	}

	fmt.Printf("  Reconstruction: all checks passed\n")

	return nil
}

// validateFrameTableOffsets checks that each mapping's FrameTable correctly
// covers the mapping's BuildStorageOffset range. This catches the "offset mixing"
// bug where FrameTable U-offsets don't match BuildStorageOffset coordinates.
func validateFrameTableOffsets(h *header.Header) error {
	fmt.Printf("  Validating FrameTable offset consistency for %d mappings\n", len(h.Mapping))

	for i, mapping := range h.Mapping {
		ft := mapping.FrameTable
		if ft == nil || len(ft.Frames) == 0 {
			continue
		}

		// The FrameTable's U range must cover [BuildStorageOffset, BuildStorageOffset+Length)
		storageStart := int64(mapping.BuildStorageOffset)
		storageEnd := storageStart + int64(mapping.Length)

		// FrameTable starts at ft.StartAt.U and covers sum of all frame U sizes
		ftStart := ft.StartAt.U
		ftEnd := ft.StartAt.U
		for _, frame := range ft.Frames {
			ftEnd += int64(frame.U)
		}

		// The FrameTable must start at or before BuildStorageOffset
		if ftStart > storageStart {
			return fmt.Errorf("mapping[%d] build=%s: FrameTable starts at U=%#x but BuildStorageOffset=%#x (FT starts AFTER mapping)",
				i, mapping.BuildId, ftStart, storageStart)
		}

		// The FrameTable must extend to or past BuildStorageOffset+Length
		if ftEnd < storageEnd {
			return fmt.Errorf("mapping[%d] build=%s: FrameTable ends at U=%#x but mapping ends at %#x (FT too short, gap=%#x)",
				i, mapping.BuildId, ftEnd, storageEnd, storageEnd-ftEnd)
		}

		// Verify FrameFor() succeeds for the mapping's start offset
		frameStart, _, err := ft.FrameFor(storageStart)
		if err != nil {
			return fmt.Errorf("mapping[%d] build=%s: FrameFor(%#x) failed: %w",
				i, mapping.BuildId, storageStart, err)
		}

		// The frame's U start must be <= BuildStorageOffset
		if frameStart.U > storageStart {
			return fmt.Errorf("mapping[%d] build=%s: frame at U=%#x but BuildStorageOffset=%#x (frame starts AFTER mapping data)",
				i, mapping.BuildId, frameStart.U, storageStart)
		}

		// Verify FrameFor() succeeds for the mapping's last byte
		if mapping.Length > 0 {
			lastByte := storageEnd - 1
			_, _, err = ft.FrameFor(lastByte)
			if err != nil {
				return fmt.Errorf("mapping[%d] build=%s: FrameFor(%#x) failed for last byte: %w",
					i, mapping.BuildId, lastByte, err)
			}
		}

		fmt.Printf("    mapping[%d] build=%s vOff=%#x storageOff=%#x len=%#x ftU=[%#x,%#x) OK\n",
			i, mapping.BuildId, mapping.Offset, storageStart, mapping.Length, ftStart, ftEnd)
	}

	fmt.Printf("  FrameTable offsets: all consistent\n")

	return nil
}

// validateCompressedFrames decompresses every frame described in the V4 header
// and verifies decompression succeeds. For each build, it reads from the
// compressed .zstd file, decompresses each frame, and computes CRC32 of the
// decompressed data. This works with compressed-only builds (no uncompressed
// original required).
func validateCompressedFrames(ctx context.Context, storagePath, artifactName string, compressedH *header.Header) error {
	// Collect unique frames to validate, keyed by (buildID, C-offset).
	type frameInfo struct {
		offset storage.FrameOffset
		size   storage.FrameSize
		ct     storage.CompressionType
	}
	type frameKey struct {
		buildID string
		cOffset int64
	}

	buildFrames := make(map[string][]frameInfo)
	seen := make(map[frameKey]bool)

	for _, mapping := range compressedH.Mapping {
		ft := mapping.FrameTable
		if !storage.IsCompressed(ft) {
			continue
		}

		bid := mapping.BuildId.String()
		if bid == cmdutil.NilUUID {
			continue
		}

		currentOffset := ft.StartAt
		for _, frame := range ft.Frames {
			key := frameKey{bid, currentOffset.C}
			if !seen[key] {
				seen[key] = true
				buildFrames[bid] = append(buildFrames[bid], frameInfo{
					offset: currentOffset,
					size:   frame,
					ct:     ft.CompressionType,
				})
			}
			currentOffset.Add(frame)
		}
	}

	if len(buildFrames) == 0 {
		fmt.Printf("  No compressed frames to validate\n")

		return nil
	}

	totalFrames := 0
	for _, frames := range buildFrames {
		totalFrames += len(frames)
	}
	fmt.Printf("  Validating %d unique compressed frames across %d builds\n", totalFrames, len(buildFrames))

	for bid, frames := range buildFrames {
		compressedFile := storage.CompressedDataName(artifactName, frames[0].ct)
		compReader, compSize, _, err := cmdutil.OpenDataFile(ctx, storagePath, bid, compressedFile)
		if err != nil {
			return fmt.Errorf("build %s: failed to open %s: %w", bid, compressedFile, err)
		}

		fmt.Printf("  Build %s: %d frames, compressed file=%s size=%#x\n", bid, len(frames), compressedFile, compSize)

		decompressedHash := md5.New()
		var totalDecompressed int64

		for i, frame := range frames {
			// Read compressed bytes
			compBuf := make([]byte, frame.size.C)
			_, err := compReader.ReadAt(compBuf, frame.offset.C)
			if err != nil {
				compReader.Close()

				return fmt.Errorf("build %s frame[%d]: read compressed at C=%#x size=%#x: %w",
					bid, i, frame.offset.C, frame.size.C, err)
			}

			// Decompress and verify
			decompressed, err := storage.DecompressFrame(frame.ct, compBuf, frame.size.U)
			if err != nil {
				previewLen := min(32, len(compBuf))
				compReader.Close()

				return fmt.Errorf("build %s frame[%d]: decompress at C=%#x (first %d bytes: %x): %w",
					bid, i, frame.offset.C, previewLen, compBuf[:previewLen], err)
			}

			if int32(len(decompressed)) != frame.size.U {
				compReader.Close()

				return fmt.Errorf("build %s frame[%d]: decompressed size %#x != expected %#x",
					bid, i, len(decompressed), frame.size.U)
			}

			decompressedHash.Write(decompressed)
			totalDecompressed += int64(frame.size.U)

			frameCRC := crc32.ChecksumIEEE(decompressed)

			if i < 5 || i == len(frames)-1 {
				fmt.Printf("    frame[%d] U=%#x C=%#x crc32=%#08x OK (%#x→%#x)\n",
					i, frame.offset.U, frame.offset.C, frameCRC, frame.size.C, frame.size.U)
			} else if i == 5 {
				fmt.Printf("    ... (%d more frames) ...\n", len(frames)-6)
			}
		}

		compReader.Close()

		decompressedMD5 := hex.EncodeToString(decompressedHash.Sum(nil))
		fmt.Printf("  Build %s: all %d frames OK, decompressed=%#x (%d MiB), MD5=%s\n",
			bid, len(frames), totalDecompressed, totalDecompressed/1024/1024, decompressedMD5)
	}

	fmt.Printf("  Compressed frames: all %d validated\n", totalFrames)

	return nil
}

// templateInfo represents a template from the E2B API.
type templateInfo struct {
	TemplateID string   `json:"templateID"`
	BuildID    string   `json:"buildID"`
	Aliases    []string `json:"aliases"`
	Names      []string `json:"names"`
}

// resolveTemplateID fetches the build ID for a template from the E2B API.
func resolveTemplateID(input string) (string, error) {
	apiKey := os.Getenv("E2B_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("E2B_API_KEY environment variable required for -template flag")
	}

	apiURL := "https://api.e2b.dev/templates"
	if domain := os.Getenv("E2B_DOMAIN"); domain != "" {
		apiURL = fmt.Sprintf("https://api.%s/templates", domain)
	}

	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("X-API-Key", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch templates: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)

		return "", fmt.Errorf("API returned %d: %s", resp.StatusCode, string(body))
	}

	var templates []templateInfo
	if err := json.NewDecoder(resp.Body).Decode(&templates); err != nil {
		return "", fmt.Errorf("failed to parse API response: %w", err)
	}

	var match *templateInfo
	var availableAliases []string

	for i := range templates {
		t := &templates[i]
		availableAliases = append(availableAliases, t.Aliases...)

		if t.TemplateID == input {
			match = t

			break
		}

		if slices.Contains(t.Aliases, input) {
			match = t

			break
		}

		if slices.Contains(t.Names, input) {
			match = t

			break
		}
	}

	if match == nil {
		return "", fmt.Errorf("template %q not found. Available aliases: %s", input, strings.Join(availableAliases, ", "))
	}

	if match.BuildID == "" || match.BuildID == cmdutil.NilUUID {
		return "", fmt.Errorf("template %q has no successful build", input)
	}

	return match.BuildID, nil
}
