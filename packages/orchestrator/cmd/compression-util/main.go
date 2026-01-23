package main

// import (
// 	"context"
// 	"flag"
// 	"fmt"
// 	"log"
// 	"os"
// 	"time"

// 	"go.opentelemetry.io/otel/metric/noop"

// 	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
// 	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
// 	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
// 	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
// 	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
// 	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
// )

// const (
// 	actionInspect    = "inspect"
// 	actionCompress   = "compress"
// 	actionDecompress = "decompress"
// )

// var (
// 	buildId = flag.String("build", "", "build id")
// 	kind    = flag.String("kind", "", "'memfile' or 'rootfs'")

// 	// inspect flags
// 	verify       = flag.Bool("verify", false, "verify by fetching and decompressing data")
// 	showFrames   = flag.Bool("show-frames", false, "show frame details")
// 	groupByBuild = flag.Bool("group-by-build", false, "group output by build id")
// )

// func main() {
// 	flag.Parse()

// 	template := storage.TemplateFiles{
// 		BuildID: *buildId,
// 	}

// 	var headerStoragePath string
// 	var objectStoragePath string
// 	var objectType storage.ObjectType
// 	var fileType build.DiffType

// 	switch *kind {
// 	case "memfile":
// 		headerStoragePath = template.StorageMemfileHeaderPath()
// 		objectStoragePath = template.StorageMemfilePath()
// 		objectType = storage.MemfileHeaderObjectType
// 		fileType = build.Memfile
// 	case "rootfs":
// 		headerStoragePath = template.StorageRootfsHeaderPath()
// 		objectStoragePath = template.StorageRootfsPath()
// 		objectType = storage.RootFSHeaderObjectType
// 		fileType = build.Rootfs
// 	default:
// 		log.Fatalf("invalid kind: %s", *kind)

// 		return
// 	}

// 	ctx := context.Background()
// 	st, err := storage.ForTemplates(ctx, nil)
// 	if err != nil {
// 		log.Fatalf("failed to get storage provider: %s", err)
// 	}

// 	obj, err := st.OpenObject(ctx, headerStoragePath, objectType)
// 	if err != nil {
// 		log.Fatalf("failed to open object: %s", err)
// 	}

// 	h, err := header.Deserialize(ctx, obj)
// 	if err != nil {
// 		log.Fatalf("failed to deserialize header: %s", err)
// 	}

// 	// Validate mappings
// 	err = header.ValidateMappings(h.Mapping, h.Metadata.Size, h.Metadata.BlockSize)
// 	if err != nil {
// 		fmt.Printf("\n⚠️  WARNING: Mapping validation failed!\n%s\n\n", err)
// 	}

// 	action := flag.Arg(0)
// 	fmt.Printf("action:%q, args:%s\n", action, flag.Args())

// 	switch action {
// 	case actionCompress:
// 		compress(h, st, headerStoragePath, objectStoragePath, fileType)
// 	case actionInspect:
// 		inspectHeader(h)
// 	default:
// 		log.Fatalf("invalid action: %s", action)
// 	}
// }

// func compress(h *header.Header, storage storage.API, headerStoragePath string, fileStoragePath string, fileType build.DiffType) {
// 	// first we need to fully download buildId

// 	ctx := context.Background()
// 	obj, err := storage.OpenFramedReader(ctx, fileStoragePath)

// 	cachePath := os.TempDir()

// 	c, err := cfg.Parse()
// 	if err != nil {
// 		log.Fatalf("failed to parse config: %s", err)
// 	}

// 	diffStore, err := build.NewDiffStore(
// 		c,
// 		nil,
// 		cachePath,
// 		25*time.Hour,
// 		60*time.Second,
// 	)

// 	metrics, err := blockmetrics.NewMetrics(&noop.MeterProvider{})
// 	if err != nil {
// 		log.Fatalf("failed to create metrics: %s", err)
// 	}

// 	ctx := context.Background()
// 	f, memfileErr := template.NewStorage(
// 		ctx,
// 		diffStore,
// 		*buildId,
// 		fileType,
// 		h,
// 		storage,
// 		metrics,
// 	)
// 	if memfileErr != nil {
// 		log.Fatalf("failed to create template storage: %s", memfileErr)
// 	}


// }
