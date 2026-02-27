package cmdutil

import (
	"fmt"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const NilUUID = "00000000-0000-0000-0000-000000000000"

// ANSI color codes for compression ratio visualization.
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[91m" // bright red — incompressible
	colorYellow = "\033[33m" // yellow — poor
	colorGreen  = "\033[32m" // green — good
	colorCyan   = "\033[36m" // cyan — very sparse
	colorBlue   = "\033[34m" // blue — nearly empty
)

// RatioColor returns an ANSI color code for a compression ratio value.
func RatioColor(ratio float64) string {
	switch {
	case ratio < 1.5:
		return colorRed
	case ratio < 2.5:
		return colorYellow
	case ratio < 4:
		return colorReset
	case ratio < 8:
		return colorGreen
	case ratio < 50:
		return colorCyan
	default:
		return colorBlue
	}
}

// FormatRatio returns a color-coded ratio string (4 chars wide).
func FormatRatio(ratio float64) string {
	color := RatioColor(ratio)
	if ratio >= 100 {
		return fmt.Sprintf("%s%4.0f%s", color, ratio, colorReset)
	}

	return fmt.Sprintf("%s%4.1f%s", color, ratio, colorReset)
}

// FormatMappingWithCompression returns mapping info with compression details.
func FormatMappingWithCompression(mapping *header.BuildMap, blockSize uint64) string {
	base := mapping.Format(blockSize)

	if mapping.FrameTable == nil {
		return base + " [uncompressed]"
	}

	ft := mapping.FrameTable
	var totalU, totalC int64
	for _, frame := range ft.Frames {
		totalU += int64(frame.U)
		totalC += int64(frame.C)
	}

	ratio := float64(totalU) / float64(totalC)

	return fmt.Sprintf("%s [%s: %d frames, U=%#x C=%#x ratio=%s]",
		base, ft.CompressionType.String(), len(ft.Frames), totalU, totalC, FormatRatio(ratio))
}

// PrintCompressionSummary prints compression statistics for a header.
func PrintCompressionSummary(h *header.Header) {
	var compressedMappings, uncompressedMappings int
	var totalUncompressedBytes, totalCompressedBytes int64
	var totalFrames int

	type buildStats struct {
		uncompressedBytes int64
		compressedBytes   int64
		frames            []storage.FrameSize
		compressed        bool
	}
	buildCompressionStats := make(map[string]*buildStats)

	for _, mapping := range h.Mapping {
		buildID := mapping.BuildId.String()
		if buildID == NilUUID {
			continue
		}

		if _, ok := buildCompressionStats[buildID]; !ok {
			buildCompressionStats[buildID] = &buildStats{}
		}
		stats := buildCompressionStats[buildID]

		if mapping.FrameTable != nil && mapping.FrameTable.CompressionType != storage.CompressionNone {
			compressedMappings++
			stats.compressed = true

			for _, frame := range mapping.FrameTable.Frames {
				totalUncompressedBytes += int64(frame.U)
				totalCompressedBytes += int64(frame.C)
				stats.uncompressedBytes += int64(frame.U)
				stats.compressedBytes += int64(frame.C)
				stats.frames = append(stats.frames, frame)
			}
			totalFrames += len(mapping.FrameTable.Frames)
		} else {
			uncompressedMappings++
			totalUncompressedBytes += int64(mapping.Length)
			stats.uncompressedBytes += int64(mapping.Length)
		}
	}

	fmt.Printf("\nCOMPRESSION SUMMARY\n")
	fmt.Printf("===================\n")

	if compressedMappings == 0 && uncompressedMappings == 0 {
		fmt.Printf("No data mappings (all sparse)\n")

		return
	}

	fmt.Printf("Mappings:          %d compressed, %d uncompressed\n", compressedMappings, uncompressedMappings)

	if compressedMappings > 0 {
		ratio := float64(totalUncompressedBytes) / float64(totalCompressedBytes)
		savings := 100.0 * (1.0 - float64(totalCompressedBytes)/float64(totalUncompressedBytes))
		fmt.Printf("Total frames:      %d\n", totalFrames)
		fmt.Printf("Uncompressed size: %#x (%.2f MiB)\n", totalUncompressedBytes, float64(totalUncompressedBytes)/1024/1024)
		fmt.Printf("Compressed size:   %#x (%.2f MiB)\n", totalCompressedBytes, float64(totalCompressedBytes)/1024/1024)
		fmt.Printf("Compression ratio: %s (%.1f%% space savings)\n", FormatRatio(ratio), savings)
	} else {
		fmt.Printf("All mappings are uncompressed\n")
	}

	hasCompressedBuilds := false
	for _, stats := range buildCompressionStats {
		if stats.compressed {
			hasCompressedBuilds = true

			break
		}
	}

	if hasCompressedBuilds {
		fmt.Printf("\nPer-build compression:\n")
		for buildID, stats := range buildCompressionStats {
			label := buildID[:8] + "..."
			if buildID == h.Metadata.BuildId.String() {
				label += " (current)"
			} else if buildID == h.Metadata.BaseBuildId.String() {
				label += " (parent)"
			}

			if !stats.compressed {
				fmt.Printf("  %s: uncompressed, %#x\n", label, stats.uncompressedBytes)

				continue
			}

			ratio := float64(stats.uncompressedBytes) / float64(stats.compressedBytes)
			fmt.Printf("  %s: %d frames, U=%#x C=%#x (%s)\n",
				label, len(stats.frames), stats.uncompressedBytes, stats.compressedBytes, FormatRatio(ratio))

			// Frame stats
			if len(stats.frames) > 0 {
				minC, maxC := stats.frames[0].C, stats.frames[0].C
				for _, f := range stats.frames[1:] {
					minC = min(minC, f.C)
					maxC = max(maxC, f.C)
				}
				avgC := stats.compressedBytes / int64(len(stats.frames))
				fmt.Printf("    Frame sizes: avg %d KiB, min %d KiB, max %d KiB\n",
					avgC/1024, minC/1024, maxC/1024)
			}

			// Ratio matrix: 16 frames per row
			if len(stats.frames) > 1 {
				const cols = 16
				fmt.Printf("\n    Ratio matrix (%d per row):\n", cols)
				for row := 0; row < len(stats.frames); row += cols {
					end := min(row+cols, len(stats.frames))
					fmt.Printf("    %4d: ", row)
					for _, f := range stats.frames[row:end] {
						r := float64(f.U) / float64(f.C)
						fmt.Printf(" %s", FormatRatio(r))
					}
					fmt.Println()
				}
				fmt.Println()
			}
		}
	}
}
