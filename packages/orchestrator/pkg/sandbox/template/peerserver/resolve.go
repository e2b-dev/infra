package peerserver

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// ErrUnknownFile is returned when the requested file name is not recognised.
var ErrUnknownFile = fmt.Errorf("unknown file")

// ResolveFramed maps (buildID, fileName) to a FramedSource.
// Supported file names: memfile, rootfs.ext4.
// Returns ErrNotAvailable when the build is not in the local cache.
// Returns ErrUnknownFile for unrecognised file names.
func ResolveFramed(cache Cache, buildID, fileName string) (FramedSource, error) {
	switch storage.BaseFileName(fileName) {
	case storage.MemfileName, storage.RootfsName:
		diff, ok := cache.LookupDiff(buildID, build.DiffType(fileName))
		if !ok {
			return nil, ErrNotAvailable
		}

		return &framedSource{diff: diff}, nil

	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownFile, fileName)
	}
}

// ResolveBlob maps (buildID, fileName) to a BlobSource.
// Supported file names: snapfile, metadata.json, memfile.header, rootfs.ext4.header.
// Returns ErrNotAvailable when the build is not in the local cache.
// Returns ErrUnknownFile for unrecognised file names.
func ResolveBlob(cache Cache, buildID, fileName string) (BlobSource, error) {
	t, ok := cache.GetCachedTemplate(buildID)
	if !ok {
		return nil, ErrNotAvailable
	}

	switch fileName {
	case storage.SnapfileName:
		return &fileSource{getFile: t.Snapfile}, nil

	case storage.MetadataName:
		return &metadataSource{getMetadata: t.Metadata}, nil

	case storage.MemfileName + storage.HeaderSuffix:
		return &headerSource{getDevice: t.Memfile}, nil

	case storage.RootfsName + storage.HeaderSuffix:
		return &headerSource{getDevice: func(_ context.Context) (block.ReadonlyDevice, error) { return t.Rootfs() }}, nil

	default:
		return nil, fmt.Errorf("%w: %q", ErrUnknownFile, fileName)
	}
}
