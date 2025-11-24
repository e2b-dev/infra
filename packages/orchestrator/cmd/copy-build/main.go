package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
	"sync/atomic"

	googleStorage "cloud.google.com/go/storage"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type Destination struct {
	Path string
	CRC  uint32
}

func NewDestinationFromObject(ctx context.Context, o *googleStorage.ObjectHandle) (*Destination, error) {
	var crc uint32
	if attrs, err := o.Attrs(ctx); err == nil {
		crc = attrs.CRC32C
	} else if !errors.Is(err, googleStorage.ErrObjectNotExist) {
		return nil, fmt.Errorf("failed to get object attributes: %w", err)
	}

	return &Destination{
		Path: fmt.Sprintf("gs://%s/%s", o.BucketName(), o.ObjectName()),
		CRC:  crc,
	}, nil
}

func NewDestinationFromPath(prefix, file string) (*Destination, error) {
	p := path.Join(prefix, file)

	if _, err := os.Stat(p); err == nil {
		f, err := os.Open(p)
		if err != nil {
			return nil, fmt.Errorf("failed to open file: %w", err)
		}
		defer f.Close()

		h := crc32.New(crc32.MakeTable(crc32.Castagnoli))
		_, err = io.Copy(h, f)
		if err != nil {
			return nil, fmt.Errorf("failed to copy file: %w", err)
		}
		crc := h.Sum32()

		return &Destination{
			Path: p,
			CRC:  crc,
		}, nil
	}

	return &Destination{
		Path: p,
	}, nil
}

func NewHeaderFromObject(ctx context.Context, bucketName string, headerPath string, objectType storage.ObjectType) (*header.Header, error) {
	b, err := storage.NewGCPBucketStorageProvider(ctx, bucketName, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCS bucket storage provider: %w", err)
	}

	obj, err := b.OpenObject(ctx, headerPath, objectType)
	if err != nil {
		return nil, fmt.Errorf("failed to open object: %w", err)
	}

	h, err := header.Deserialize(ctx, obj)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize header: %w", err)
	}

	return h, nil
}

type osFileWriterToCtx struct {
	f *os.File
}

func (o *osFileWriterToCtx) WriteTo(_ context.Context, w io.Writer) (int64, error) {
	return io.Copy(w, o.f)
}

func NewHeaderFromPath(ctx context.Context, headerPath string) (*header.Header, error) {
	f, err := os.Open(headerPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	h, err := header.Deserialize(ctx, &osFileWriterToCtx{f: f})
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize header: %w", err)
	}

	return h, nil
}

func getReferencedData(h *header.Header, objectType storage.ObjectType) ([]string, error) {
	builds := make(map[string]struct{})

	for _, mapping := range h.Mapping {
		builds[mapping.BuildId.String()] = struct{}{}
	}

	delete(builds, uuid.Nil.String())

	var dataReferences []string

	for build := range builds {
		template := storage.TemplateFiles{
			BuildID:            build,
			KernelVersion:      "",
			FirecrackerVersion: "",
		}

		switch objectType {
		case storage.MemfileHeaderObjectType:
			dataReferences = append(dataReferences, template.StorageMemfilePath())
		case storage.RootFSHeaderObjectType:
			dataReferences = append(dataReferences, template.StorageRootfsPath())
		}
	}

	return dataReferences, nil
}

func gcloudCopy(ctx context.Context, from, to *Destination) (bool, error) {
	if from.CRC == to.CRC && from.CRC != 0 {
		return false, nil
	}

	command := []string{
		"gcloud",
		"storage",
		"cp",
		"--verbosity",
		"error",
		from.Path,
		to.Path,
	}

	cmd := exec.CommandContext(ctx, command[0], command[1:]...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("failed to copy GCS object (%v): %w\n%s", command, err, string(output))
	}

	return true, nil
}

func main() {
	buildId := flag.String("build", "", "build id")
	from := flag.String("from", "", "from bucket")
	to := flag.String("to", "", "to destination")

	flag.Parse()

	fmt.Printf("Copying build '%s' from '%s' to '%s'\n", *buildId, *from, *to)

	template := storage.TemplateFiles{
		BuildID:            *buildId,
		KernelVersion:      "",
		FirecrackerVersion: "",
	}

	ctx := context.Background()

	var filesToCopy []string

	// Extract all files referenced by the build memfile header
	buildMemfileHeaderPath := template.StorageMemfileHeaderPath()

	var memfileHeader *header.Header
	if strings.HasPrefix(*from, "gs://") {
		bucketName, _ := strings.CutPrefix(*from, "gs://")

		h, err := NewHeaderFromObject(ctx, bucketName, buildMemfileHeaderPath, storage.MemfileHeaderObjectType)
		if err != nil {
			log.Fatalf("failed to create header from object: %s", err)
		}

		memfileHeader = h
	} else {
		h, err := NewHeaderFromPath(ctx, buildMemfileHeaderPath)
		if err != nil {
			log.Fatalf("failed to create header from path: %s", err)
		}

		memfileHeader = h
	}

	dataReferences, err := getReferencedData(memfileHeader, storage.MemfileHeaderObjectType)
	if err != nil {
		log.Fatalf("failed to get referenced data: %s", err)
	}

	filesToCopy = append(filesToCopy, buildMemfileHeaderPath)
	filesToCopy = append(filesToCopy, dataReferences...)

	// Extract all files referenced by the build rootfs header
	buildRootfsHeaderPath := template.StorageRootfsHeaderPath()

	var rootfsHeader *header.Header
	if strings.HasPrefix(*from, "gs://") {
		bucketName, _ := strings.CutPrefix(*from, "gs://")
		h, err := NewHeaderFromObject(ctx, bucketName, buildRootfsHeaderPath, storage.RootFSHeaderObjectType)
		if err != nil {
			log.Fatalf("failed to create header from object: %s", err)
		}

		rootfsHeader = h
	} else {
		h, err := NewHeaderFromPath(ctx, buildRootfsHeaderPath)
		if err != nil {
			log.Fatalf("failed to create header from path: %s", err)
		}

		rootfsHeader = h
	}

	dataReferences, err = getReferencedData(rootfsHeader, storage.RootFSHeaderObjectType)
	if err != nil {
		log.Fatalf("failed to get referenced data: %s", err)
	}

	filesToCopy = append(filesToCopy, buildRootfsHeaderPath)
	filesToCopy = append(filesToCopy, dataReferences...)

	// Add the snapfile to the list of files to copy
	snapfilePath := template.StorageSnapfilePath()
	filesToCopy = append(filesToCopy, snapfilePath)

	metadataPath := template.StorageMetadataPath()
	filesToCopy = append(filesToCopy, metadataPath)

	// sort files to copy
	sort.Strings(filesToCopy)

	googleStorageClient, err := googleStorage.NewClient(ctx)
	if err != nil {
		log.Fatalf("failed to create Google Storage client: %s", err)
	}

	fmt.Printf("Copying %d files\n", len(filesToCopy))

	var errgroup errgroup.Group

	errgroup.SetLimit(20)

	var done atomic.Int32

	for _, file := range filesToCopy {
		errgroup.Go(func() error {
			var fromDestination *Destination
			if strings.HasPrefix(*from, "gs://") {
				bucketName, _ := strings.CutPrefix(*from, "gs://")
				fromObject := googleStorageClient.Bucket(bucketName).Object(file)
				fromDestination, err = NewDestinationFromObject(ctx, fromObject)
				if err != nil {
					return fmt.Errorf("failed to create destination from object: %w", err)
				}
			} else {
				fromDestination, err = NewDestinationFromPath(*from, file)
				if err != nil {
					return fmt.Errorf("failed to create destination from path: %w", err)
				}
			}

			var toDestination *Destination
			if strings.HasPrefix(*to, "gs://") {
				bucketName, _ := strings.CutPrefix(*to, "gs://")
				toObject := googleStorageClient.Bucket(bucketName).Object(file)
				toDestination, err = NewDestinationFromObject(ctx, toObject)
				if err != nil {
					return fmt.Errorf("failed to create destination from object: %w", err)
				}
			} else {
				toDestination, err = NewDestinationFromPath(*to, file)
				if err != nil {
					return fmt.Errorf("failed to create destination from path: %w", err)
				}

				err = os.MkdirAll(path.Dir(toDestination.Path), 0o755)
				if err != nil {
					return fmt.Errorf("failed to create directory: %w", err)
				}
			}

			fmt.Printf("+ copying '%s' to '%s'\n", fromDestination.Path, toDestination.Path)

			copied, err := gcloudCopy(ctx, fromDestination, toDestination)
			if err != nil {
				fmt.Fprintf(os.Stderr, "- failed to copy '%s': %s\n", file, err)

				return err
			}

			done.Add(1)

			if copied {
				fmt.Printf("-> [%d/%d] '%s' copied\n", done.Load(), len(filesToCopy), toDestination.Path)
			} else {
				fmt.Printf("-> [%d/%d] '%s' already exists, skipping\n", done.Load(), len(filesToCopy), toDestination.Path)
			}

			return nil
		})
	}

	if err := errgroup.Wait(); err != nil {
		log.Fatalf("failed to copy files: %s", err)
	}

	fmt.Printf("Build '%s' copied to bucket '%s'\n", *buildId, *to)
}
