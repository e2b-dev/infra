package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func inspectHeader(h *header.Header) {
	if h.Metadata.Version < 4 {
		fmt.Printf("Header version %d: no compression support (4+ required)\n", h.Metadata.Version)
		return
	}

	builds := make(map[string]struct{})
	for _, m := range h.Mapping {
		builds[m.BuildId.String()] = struct{}{}
	}

	_, currentBuildPresentInBindings := builds[*buildId]
	if !currentBuildPresentInBindings {
		fmt.Printf("\n⚠️  WARNING: current build ID is not present in header mappings, %d other builds found\n\n", len(builds))
	}
	delete(builds, *buildId)

	sortedBuilds := make([]string, 0, len(builds))
	for b := range builds {
		sortedBuilds = append(sortedBuilds, b)
	}
	sort.Strings(sortedBuilds)
	if currentBuildPresentInBindings {
		sortedBuilds = append([]string{*buildId}, sortedBuilds...)
	}

	if *groupByBuild {
		for _, b := range sortedBuilds {
			if b == *buildId {
				fmt.Printf("\nBUILD ID (current): %s\n", b)
			} else {
				fmt.Printf("\nBUILD ID: %s\n", b)
			}
			for _, m := range h.Mapping {
				if m.BuildId.String() != b {
					continue
				}
				inspectMapping(m)
			}
		}
	} else {
		for _, m := range h.Mapping {
			inspectMapping(m)
		}
	}
}

func inspectMapping(m *header.BuildMap) {
	fmt.Printf("  %#x (%#x), BuildId: %s at %#x",
		m.Offset, m.Length, m.BuildId, m.BuildStorageOffset)

	if m.FrameTable != nil {
		if len(m.FrameTable.Frames) == 0 {
			fmt.Printf("\n⚠️  ERROR: Compressed mapping with no frames!\n")
			os.Exit(1)
		}

		var framesOutput strings.Builder
		totalC, totalU := uint64(0), uint64(0)
		compressedStorageOffset := m.FrameTable.StartAt
		for _, f := range m.FrameTable.Frames {
			if *showFrames {
				framesOutput.WriteString(fmt.Sprintf("    Frame at %x(%x): %x->%x bytes, %d%% reduction\n",
					compressedStorageOffset.U, compressedStorageOffset.C, f.C, f.U, 100-((f.C*100)/f.U)))
			}
			compressedStorageOffset.C += int64(f.C)
			compressedStorageOffset.U += int64(f.U)
			totalC += uint64(f.C)
			totalU += uint64(f.U)

			// TODO: if verify is set, fetch and decompress each frame here
		}

		fmt.Printf(", compressed as %s: %x->%x bytes (%d%% reduction)",
			m.FrameTable.CompressionType,
			totalU, totalC, 100-((totalC*100)/totalU))

	}
	fmt.Printf("\n")
}
