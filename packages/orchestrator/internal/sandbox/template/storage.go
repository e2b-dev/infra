package template

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const (
	oldMemfileHugePageSize = 2 << 20 // 2 MiB
	oldRootfsBlockSize     = 2 << 11 // 4 KiB
)

type Storage struct {
	header *header.Header
	source *build.File
}

func NewStorage(
	ctx context.Context,
	store *build.DiffStore,
	buildId string,
	fileType build.DiffType,
	h *header.Header,
	persistence storage.StorageProvider,
) (*Storage, error) {
	if h == nil {
		headerObjectPath := buildId + "/" + string(fileType) + storage.HeaderSuffix
		headerObject, err := persistence.OpenObject(ctx, headerObjectPath)
		if err != nil {
			return nil, err
		}

		diffHeader, err := header.Deserialize(headerObject)

		// If we can't find the diff header in storage, we switch to templates without a headers
		if err != nil && !errors.Is(err, storage.ErrorObjectNotExist) {
			return nil, fmt.Errorf("failed to deserialize header: %w", err)
		}

		if err == nil {
			h = diffHeader
		}
	}

	// If we can't find the diff header in storage, we try to find the "old" style template without a header as a fallback.
	if h == nil {
		objectPath := buildId + "/" + string(fileType)
		object, err := persistence.OpenObject(ctx, objectPath)
		if err != nil {
			return nil, err
		}

		size, err := object.Size()
		if err != nil {
			return nil, fmt.Errorf("failed to get object size: %w", err)
		}

		id, err := uuid.Parse(buildId)
		if err != nil {
			return nil, fmt.Errorf("failed to parse build id: %w", err)
		}

		// TODO: This is a workaround for the old style template without a header.
		// We don't know the block size of the old style template, so we set it manually.
		var blockSize uint64
		switch fileType {
		case build.Memfile:
			blockSize = oldMemfileHugePageSize
		case build.Rootfs:
			blockSize = oldRootfsBlockSize
		default:
			return nil, fmt.Errorf("unsupported file type: %s", fileType)
		}

		h = header.NewHeader(&header.Metadata{
			BuildId:     id,
			BaseBuildId: id,
			Size:        uint64(size),
			Version:     1,
			BlockSize:   blockSize,
			Generation:  1,
		}, nil)
	}

	b := build.NewFile(h, store, fileType, persistence)

	return &Storage{
		source: b,
		header: h,
	}, nil
}

func (d *Storage) ReadAt(p []byte, off int64) (int, error) {
	return d.source.ReadAt(p, off)
}

func (d *Storage) Size() (int64, error) {
	return int64(d.header.Metadata.Size), nil
}

func (d *Storage) BlockSize() int64 {
	return int64(d.header.Metadata.BlockSize)
}

func (d *Storage) Slice(off, length int64) ([]byte, error) {
	return d.source.Slice(off, length)
}

func (d *Storage) Header() *header.Header {
	return d.header
}

func (d *Storage) Close() error {
	return nil
}
