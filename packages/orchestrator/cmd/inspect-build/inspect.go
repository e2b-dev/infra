package main

import (
	"context"
	"flag"
	"log"
	"os"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/e2b-dev/infra/packages/orchestrator/cmd/internal/cmdutil"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

var expandSections = []string{sectionMappings, sectionFrames, sectionMetadata, sectionAll}

// runInspect is the entry point for the redesigned dashboard CLI. main() in
// main.go dispatches here unless --old selects the legacy header-dump tool.
func runInspect() {
	build := flag.String("build", "", "build ID")
	template := flag.String("template", "", "template ID or alias (requires E2B_API_KEY)")
	storagePath := flag.String("storage", ".local-build", "storage: local path or gs://bucket")
	memfile := flag.Bool("memfile", false, "inspect memfile artifact (default)")
	rootfs := flag.Bool("rootfs", false, "inspect rootfs artifact")
	human := flag.Bool("human", false, "force the human dashboard")
	jsonOut := flag.Bool("json", false, "force JSON output")
	decimal := flag.Bool("decimal", false, "human mode: exact offsets/sizes in decimal, not hex")
	expand := flag.String("expand", "", "sections to show in full: "+strings.Join(expandSections, ","))
	rangeArg := flag.String("range", "", "limit expanded mapping/frame lists to offset:size")
	validate := flag.Bool("validate", false, "fetch+decompress every frame via the production read path and verify the checksum")
	recursive := flag.Bool("recursive", false, "also inspect the full ancestor chain")
	flag.Parse()

	// Keep the standard log enabled — the tool reports fatal errors via
	// log.Fatal; only the zap/OTEL/LaunchDarkly noise is suppressed.
	cmdutil.SuppressNoisyLogsKeepStdLog()

	buildID, artifact := resolveTarget(*build, *template, *memfile, *rootfs)
	ctx := context.Background()

	if *validate {
		if err := runValidate(ctx, *storagePath, buildID, artifact, *recursive); err != nil {
			log.Fatalf("validate: %s", err)
		}

		return
	}

	vw := view{
		decimal: *decimal,
		expand:  parseExpand(*expand),
		rng:     parseRange(*rangeArg),
		width:   detectTermWidth(os.Stdout),
	}

	chain, err := gatherChain(ctx, *storagePath, buildID, artifact, *recursive)
	if err != nil {
		log.Fatalf("%s", err)
	}

	if *human || (!*jsonOut && isTTY(os.Stdout)) {
		renderHuman(os.Stdout, chain, vw)

		return
	}
	if err := renderJSON(os.Stdout, jsonValue(chain, vw, *recursive)); err != nil {
		log.Fatal(err)
	}
}

// jsonValue builds the JSON payload: a dependency-ordered array of reports for
// --recursive, or the single report otherwise.
func jsonValue(chain []*report, vw view, recursive bool) any {
	if !recursive {
		return filterReport(chain[0], vw)
	}

	out := make([]*report, len(chain))
	for i, r := range chain {
		out[i] = filterReport(r, vw)
	}

	return out
}

// resolveTarget validates the target flags and returns the build ID to inspect
// and the artifact name (memfile or rootfs).
func resolveTarget(build, template string, memfile, rootfs bool) (buildID, artifact string) {
	switch {
	case build == "" && template == "":
		log.Fatal("specify -build or -template")
	case build != "" && template != "":
		log.Fatal("specify either -build or -template, not both")
	}

	buildID = build
	if template != "" {
		resolved, err := cmdutil.ResolveTemplateID(template)
		if err != nil {
			log.Fatalf("resolve template: %s", err)
		}
		buildID = resolved
	}

	if memfile && rootfs {
		log.Fatal("specify either -memfile or -rootfs, not both")
	}
	artifact = storage.MemfileName
	if rootfs {
		artifact = storage.RootfsName
	}

	return buildID, artifact
}

// parseExpand parses the comma-separated --expand list into a section set.
func parseExpand(s string) map[string]bool {
	expand := map[string]bool{}
	if s == "" {
		return expand
	}
	for name := range strings.SplitSeq(s, ",") {
		name = strings.TrimSpace(name)
		if !slices.Contains(expandSections, name) {
			log.Fatalf("--expand: unknown section %q (valid: %s)", name, strings.Join(expandSections, ", "))
		}
		expand[name] = true
	}

	return expand
}

// parseRange parses the --range offset:size value; both numbers accept 0x hex.
func parseRange(s string) span {
	if s == "" {
		return span{}
	}

	offStr, sizeStr, ok := strings.Cut(s, ":")
	if !ok {
		log.Fatalf("--range: expected offset:size, got %q", s)
	}
	offset, err := strconv.ParseUint(strings.TrimSpace(offStr), 0, 64)
	if err != nil {
		log.Fatalf("--range: %s", err)
	}
	size, err := strconv.ParseUint(strings.TrimSpace(sizeStr), 0, 64)
	if err != nil {
		log.Fatalf("--range: %s", err)
	}

	return span{set: true, start: offset, end: offset + size}
}

func isTTY(f *os.File) bool {
	fi, err := f.Stat()

	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// detectTermWidth queries the terminal for its column count; falls back to a
// sensible default when stdout is piped or the syscall fails.
func detectTermWidth(f *os.File) int {
	if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 20 {
		return w
	}

	return 100
}
