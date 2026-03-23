package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"hash/crc32"
	"slices"
	"sort"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/orchestrator/cmd/internal/cmdutil"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func validateArtifact(ctx context.Context, provider storage.StorageProvider, buildID, artifactName string) error {
	fmt.Printf("\n=== Validating %s for build %s ===\n", artifactName, buildID)

	headerPath := storage.TemplateFiles{BuildID: buildID}.HeaderPath(artifactName)

	h, err := header.LoadHeader(ctx, provider, headerPath)
	if err != nil {
		return fmt.Errorf("failed to load header: %w", err)
	}
	fmt.Printf("  Header: version=%d size=%#x blockSize=%#x mappings=%d\n",
		h.Metadata.Version, h.Metadata.Size, h.Metadata.BlockSize, len(h.Mapping))

	if err := header.ValidateHeader(h); err != nil {
		return fmt.Errorf("header validation failed: %w", err)
	}
	fmt.Printf("  Mappings: coverage validated\n")

	if h.Metadata.Version >= header.MetadataVersionCompressed {
		if err := validateFrameTableOffsets(h); err != nil {
			return fmt.Errorf("frame table offset validation failed: %w", err)
		}
	}

	if err := validateDataCoverage(ctx, provider, artifactName, h); err != nil {
		return fmt.Errorf("data coverage validation failed: %w", err)
	}

	if h.Metadata.Version >= header.MetadataVersionCompressed {
		if err := validateCompressedFrames(ctx, provider, artifactName, h); err != nil {
			return fmt.Errorf("compressed frame validation failed: %w", err)
		}
	}

	return nil
}

type interval struct {
	Start  int64
	Length int64
}

func (iv interval) End() int64 { return iv.Start + iv.Length }

func checkNoOverlap(intervals []interval, label string) error {
	sort.Slice(intervals, func(i, j int) bool {
		return intervals[i].Start < intervals[j].Start
	})

	for i := 1; i < len(intervals); i++ {
		prev := intervals[i-1]
		cur := intervals[i]
		if cur.Start < prev.End() {
			return fmt.Errorf("%s: overlap — interval[%d] [%#x, %#x) overlaps interval[%d] [%#x, %#x)",
				label, i-1, prev.Start, prev.End(), i, cur.Start, cur.End())
		}
	}

	return nil
}

func checkWithinBounds(intervals []interval, size int64, label string) error {
	for i, iv := range intervals {
		if iv.Start < 0 {
			return fmt.Errorf("%s: interval[%d] starts at negative offset %#x", label, i, iv.Start)
		}
		if iv.End() > size {
			return fmt.Errorf("%s: interval[%d] [%#x, %#x) exceeds file size %#x",
				label, i, iv.Start, iv.End(), size)
		}
	}

	return nil
}

func validateDataCoverage(ctx context.Context, provider storage.StorageProvider, artifactName string, h *header.Header) error {
	type buildInfo struct {
		uIntervals []interval
		compressed bool
	}
	builds := make(map[uuid.UUID]*buildInfo)

	for _, mapping := range h.Mapping {
		if mapping.BuildId == uuid.Nil {
			continue
		}

		info, ok := builds[mapping.BuildId]
		if !ok {
			info = &buildInfo{}
			builds[mapping.BuildId] = info
		}

		info.uIntervals = append(info.uIntervals, interval{
			Start:  int64(mapping.BuildStorageOffset),
			Length: int64(mapping.Length),
		})

		if mapping.FrameTable.IsCompressed() {
			info.compressed = true
		}
	}

	fmt.Printf("  Validating data coverage for %d builds\n", len(builds))

	for bid, info := range builds {
		label := bid.String()[:8] + "..."
		tf := storage.TemplateFiles{BuildID: bid.String()}

		if info.compressed {
			if err := checkNoOverlap(info.uIntervals, label+" U-space"); err != nil {
				return err
			}
			fmt.Printf("    %s: U-space OK — %d intervals, no overlaps\n",
				label, len(info.uIntervals))

			seen := make(map[int64]bool)
			var cIntervals []interval
			for _, mapping := range h.Mapping {
				if mapping.BuildId != bid || !mapping.FrameTable.IsCompressed() {
					continue
				}
				offset := mapping.FrameTable.StartAt
				for _, frame := range mapping.FrameTable.Frames {
					if !seen[offset.C] {
						seen[offset.C] = true
						cIntervals = append(cIntervals, interval{
							Start:  offset.C,
							Length: int64(frame.C),
						})
					}
					offset.Add(frame)
				}
			}

			if err := checkNoOverlap(cIntervals, label+" C-space"); err != nil {
				return err
			}
			fmt.Printf("    %s: C-space OK — %d frames, no overlaps\n",
				label, len(cIntervals))
		} else {
			dataPath := tf.DataPath(artifactName)
			ff, err := provider.OpenFramedFile(ctx, dataPath)
			if err != nil {
				return fmt.Errorf("%s: failed to open %s: %w", label, dataPath, err)
			}
			dataSize, err := ff.Size(ctx)
			if err != nil {
				return fmt.Errorf("%s: failed to get size of %s: %w", label, dataPath, err)
			}

			if err := checkNoOverlap(info.uIntervals, label+" U-space"); err != nil {
				return err
			}
			if err := checkWithinBounds(info.uIntervals, dataSize, label+" U-space"); err != nil {
				return err
			}
			fmt.Printf("    %s: U-space OK — %d intervals, no overlaps, within [0, %#x)\n",
				label, len(info.uIntervals), dataSize)
		}
	}

	fmt.Printf("  Data coverage: all builds validated\n")

	return nil
}

func validateFrameTableOffsets(h *header.Header) error {
	fmt.Printf("  Validating FrameTable offset consistency for %d mappings\n", len(h.Mapping))

	for i, mapping := range h.Mapping {
		ft := mapping.FrameTable
		if ft == nil || len(ft.Frames) == 0 {
			continue
		}

		storageStart := int64(mapping.BuildStorageOffset)
		storageEnd := storageStart + int64(mapping.Length)

		ftStart := ft.StartAt.U
		ftEnd := ft.StartAt.U
		for _, frame := range ft.Frames {
			ftEnd += int64(frame.U)
		}

		if ftStart > storageStart {
			return fmt.Errorf("mapping[%d] build=%s: FrameTable starts at U=%#x but BuildStorageOffset=%#x (FT starts AFTER mapping)",
				i, mapping.BuildId, ftStart, storageStart)
		}

		if ftEnd < storageEnd {
			return fmt.Errorf("mapping[%d] build=%s: FrameTable ends at U=%#x but mapping ends at %#x (FT too short, gap=%#x)",
				i, mapping.BuildId, ftEnd, storageEnd, storageEnd-ftEnd)
		}

		frameStart, _, err := ft.FrameFor(storageStart)
		if err != nil {
			return fmt.Errorf("mapping[%d] build=%s: FrameFor(%#x) failed: %w",
				i, mapping.BuildId, storageStart, err)
		}

		if frameStart.U > storageStart {
			return fmt.Errorf("mapping[%d] build=%s: frame at U=%#x but BuildStorageOffset=%#x (frame starts AFTER mapping data)",
				i, mapping.BuildId, frameStart.U, storageStart)
		}

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

func validateCompressedFrames(ctx context.Context, provider storage.StorageProvider, artifactName string, h *header.Header) error {
	type buildEntry struct {
		ct     storage.CompressionType
		frames []struct {
			offset storage.FrameOffset
			size   storage.FrameSize
			ft     *storage.FrameTable
		}
	}
	builds := make(map[string]*buildEntry)

	for _, mapping := range h.Mapping {
		ft := mapping.FrameTable
		if !ft.IsCompressed() {
			continue
		}
		bid := mapping.BuildId.String()
		if bid == cmdutil.NilUUID {
			continue
		}

		entry, ok := builds[bid]
		if !ok {
			entry = &buildEntry{ct: ft.CompressionType()}
			builds[bid] = entry
		}

		offset := ft.StartAt
		for _, frame := range ft.Frames {
			entry.frames = append(entry.frames, struct {
				offset storage.FrameOffset
				size   storage.FrameSize
				ft     *storage.FrameTable
			}{offset: offset, size: frame, ft: ft})
			offset.Add(frame)
		}
	}

	if len(builds) == 0 {
		fmt.Printf("  No compressed frames to validate\n")

		return nil
	}

	fmt.Printf("  Validating compressed data for %d builds\n", len(builds))

	for bid, entry := range builds {
		// Dedup frames by C offset (subsetted FTs may repeat frames)
		seen := make(map[int64]bool)
		var frames []struct {
			offset storage.FrameOffset
			size   storage.FrameSize
			ft     *storage.FrameTable
		}
		for _, f := range entry.frames {
			if !seen[f.offset.C] {
				seen[f.offset.C] = true
				frames = append(frames, f)
			}
		}

		slices.SortFunc(frames, func(a, b struct {
			offset storage.FrameOffset
			size   storage.FrameSize
			ft     *storage.FrameTable
		},
		) int {
			if a.offset.C < b.offset.C {
				return -1
			}
			if a.offset.C > b.offset.C {
				return 1
			}

			return 0
		})

		compressedFile := storage.CompressedDataName(artifactName, entry.ct)
		compPath := storage.TemplateFiles{BuildID: bid}.DataPath(compressedFile)
		ff, err := provider.OpenFramedFile(ctx, compPath)
		if err != nil {
			return fmt.Errorf("build %s: failed to open %s: %w", bid, compressedFile, err)
		}

		fmt.Printf("  Build %s: %d frames, file=%s\n", bid, len(frames), compressedFile)

		decompressedHash := sha256.New()
		var totalDecompressed int64

		for i, frame := range frames {
			decompressed := make([]byte, frame.size.U)
			_, err := ff.GetFrame(ctx, frame.offset.U, frame.ft, true, decompressed, int64(frame.size.U), nil)
			if err != nil {
				return fmt.Errorf("build %s frame[%d]: GetFrame at U=%#x: %w",
					bid, i, frame.offset.U, err)
			}

			decompressedHash.Write(decompressed)
			totalDecompressed += int64(frame.size.U)

			frameCRC := crc32.ChecksumIEEE(decompressed)
			if i < 5 || i == len(frames)-1 {
				fmt.Printf("    frame[%d] U=%#x C=%#x crc32=%#08x OK (%#x->%#x)\n",
					i, frame.offset.U, frame.offset.C, frameCRC, frame.size.C, frame.size.U)
			} else if i == 5 {
				fmt.Printf("    ... (%d more frames) ...\n", len(frames)-6)
			}
		}

		var computedChecksum [32]byte
		copy(computedChecksum[:], decompressedHash.Sum(nil))

		fmt.Printf("  Build %s: all %d frames OK, decompressed=%#x (%d MiB), SHA256=%x\n",
			bid, len(frames), totalDecompressed, totalDecompressed/1024/1024, computedChecksum)

		buildUUID, _ := uuid.Parse(bid)
		if info, ok := h.BuildFiles[buildUUID]; ok && info.Checksum != [32]byte{} {
			if computedChecksum != info.Checksum {
				return fmt.Errorf("build %s: SHA-256 mismatch: computed %x, header says %x",
					bid, computedChecksum, info.Checksum)
			}
			fmt.Printf("  Build %s: SHA-256 checksum VERIFIED\n", bid)
		}
	}

	fmt.Printf("  Compressed frames: all builds validated\n")

	return nil
}
