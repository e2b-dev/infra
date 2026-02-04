package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"slices"
	"strings"
	"unsafe"

	"github.com/e2b-dev/infra/packages/orchestrator/cmd/internal/cmdutil"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const nilUUID = "00000000-0000-0000-0000-000000000000"

func main() {
	build := flag.String("build", "", "build ID")
	template := flag.String("template", "", "template ID or alias (requires E2B_API_KEY)")
	storagePath := flag.String("storage", ".local-build", "storage: local path or gs://bucket")
	memfile := flag.Bool("memfile", false, "inspect memfile artifact")
	rootfs := flag.Bool("rootfs", false, "inspect rootfs artifact")
	data := flag.Bool("data", false, "inspect data blocks (default: header only)")
	start := flag.Int64("start", 0, "start block (only with -data)")
	end := flag.Int64("end", 0, "end block, 0 = all (only with -data)")

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

	// Determine artifact type
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
		artifactName = "rootfs"
	}

	ctx := context.Background()

	// Read header
	headerFile := artifactName + ".header"
	headerData, headerSource, err := cmdutil.ReadFile(ctx, *storagePath, *build, headerFile)
	if err != nil {
		log.Fatalf("failed to read header: %s", err)
	}

	h, err := header.DeserializeBytes(headerData)
	if err != nil {
		log.Fatalf("failed to deserialize header: %s", err)
	}

	// Print header info
	printHeader(h, headerSource)

	// If -data flag, also inspect data blocks
	if *data {
		dataFile := artifactName
		inspectData(ctx, *storagePath, *build, dataFile, h, *start, *end)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage: inspect-build (-build <uuid> | -template <id-or-alias>) [-storage <path>] [-memfile|-rootfs] [-data [-start N] [-end N]]\n\n")
	fmt.Fprintf(os.Stderr, "The -template flag requires E2B_API_KEY environment variable.\n")
	fmt.Fprintf(os.Stderr, "Set E2B_DOMAIN for non-production environments.\n\n")
	fmt.Fprintf(os.Stderr, "Examples:\n")
	fmt.Fprintf(os.Stderr, "  inspect-build -build abc123                           # inspect memfile header\n")
	fmt.Fprintf(os.Stderr, "  inspect-build -template base -storage gs://bucket     # inspect by template alias\n")
	fmt.Fprintf(os.Stderr, "  inspect-build -template gtjfpksmxd9ct81x1f8e          # inspect by template ID\n")
	fmt.Fprintf(os.Stderr, "  inspect-build -build abc123 -rootfs                   # inspect rootfs header\n")
	fmt.Fprintf(os.Stderr, "  inspect-build -build abc123 -data                     # inspect memfile header + data\n")
	fmt.Fprintf(os.Stderr, "  inspect-build -build abc123 -rootfs -data -end 100    # inspect rootfs header + first 100 blocks\n")
	fmt.Fprintf(os.Stderr, "  inspect-build -build abc123 -storage gs://bucket      # inspect from GCS\n")
}

func printHeader(h *header.Header, source string) {
	// Validate mappings
	err := header.ValidateMappings(h.Mapping, h.Metadata.Size, h.Metadata.BlockSize)
	if err != nil {
		fmt.Printf("\n⚠️  WARNING: Mapping validation failed!\n%s\n\n", err)
	}

	fmt.Printf("\nMETADATA\n")
	fmt.Printf("========\n")
	fmt.Printf("Source             %s\n", source)
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

	for _, mapping := range h.Mapping {
		fmt.Println(mapping.Format(h.Metadata.BlockSize))
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
		case nilUUID:
			additionalInfo = " (sparse)"
		}
		fmt.Printf("%s%s: %d blocks, %d MiB (%0.2f%%)\n", buildID, additionalInfo, uint64(size)/h.Metadata.BlockSize, uint64(size)/1024/1024, float64(size)/float64(h.Metadata.Size)*100)
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
	fmt.Printf("Size               %d B (%d MiB)\n", size, size/1024/1024)

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
			fmt.Printf("%-10d [%11d,%11d) %d non-zero bytes\n", i/blockSize, i, i+blockSize, nonZeroCount)
		} else {
			emptyCount++
			fmt.Printf("%-10d [%11d,%11d) EMPTY\n", i/blockSize, i, i+blockSize)
		}
	}

	fmt.Printf("\nDATA SUMMARY\n")
	fmt.Printf("============\n")
	fmt.Printf("Empty blocks: %d\n", emptyCount)
	fmt.Printf("Non-empty blocks: %d\n", nonEmptyCount)
	fmt.Printf("Total blocks inspected: %d\n", emptyCount+nonEmptyCount)
	fmt.Printf("Total size inspected: %d B (%d MiB)\n", int64(emptyCount+nonEmptyCount)*blockSize, int64(emptyCount+nonEmptyCount)*blockSize/1024/1024)
	fmt.Printf("Empty size: %d B (%d MiB)\n", int64(emptyCount)*blockSize, int64(emptyCount)*blockSize/1024/1024)

	reader.Close()
}

// templateInfo represents a template from the E2B API.
type templateInfo struct {
	TemplateID string   `json:"templateID"`
	BuildID    string   `json:"buildID"`
	Aliases    []string `json:"aliases"`
	Names      []string `json:"names"`
}

// resolveTemplateID fetches the build ID for a template from the E2B API.
// Input can be a template ID, alias, or full name (e.g., "e2b/base").
func resolveTemplateID(input string) (string, error) {
	apiKey := os.Getenv("E2B_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("E2B_API_KEY environment variable required for -template flag")
	}

	// Determine API URL
	apiURL := "https://api.e2b.dev/templates"
	if domain := os.Getenv("E2B_DOMAIN"); domain != "" {
		apiURL = fmt.Sprintf("https://api.%s/templates", domain)
	}

	// Make HTTP request
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

	// Parse response
	var templates []templateInfo
	if err := json.NewDecoder(resp.Body).Decode(&templates); err != nil {
		return "", fmt.Errorf("failed to parse API response: %w", err)
	}

	// Find matching template
	var match *templateInfo
	var availableAliases []string

	for i := range templates {
		t := &templates[i]

		// Collect aliases for error message
		availableAliases = append(availableAliases, t.Aliases...)

		// Match by template ID
		if t.TemplateID == input {
			match = t

			break
		}

		// Match by alias
		if slices.Contains(t.Aliases, input) {
			match = t

			break
		}

		// Match by full name (e.g., "e2b/base")
		if slices.Contains(t.Names, input) {
			match = t

			break
		}
	}

	if match == nil {
		return "", fmt.Errorf("template %q not found. Available aliases: %s", input, strings.Join(availableAliases, ", "))
	}

	if match.BuildID == "" || match.BuildID == nilUUID {
		return "", fmt.Errorf("template %q has no successful build", input)
	}

	return match.BuildID, nil
}
