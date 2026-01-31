package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// PrefetchEntriesToMapping converts a slice of PrefetchBlockEntry to MemoryPrefetchMapping.
// Entries are sorted by access order. Returns nil if empty.
func PrefetchEntriesToMapping(entries []block.PrefetchBlockEntry, blockSize int64) *MemoryPrefetchMapping {
	if len(entries) == 0 {
		return nil
	}

	// Sort by access order
	slices.SortFunc(entries, func(a, b block.PrefetchBlockEntry) int {
		if a.Order < b.Order {
			return -1
		}
		if a.Order > b.Order {
			return 1
		}

		return 0
	})

	orderedIndices := make([]uint64, len(entries))
	accessTypes := make([]AccessType, len(entries))
	for i, entry := range entries {
		orderedIndices[i] = entry.Index
		accessTypes[i] = AccessTypeFromBlock(entry.AccessType)
	}

	return &MemoryPrefetchMapping{
		Indices:     orderedIndices,
		AccessTypes: accessTypes,
		BlockSize:   blockSize,
	}
}

// UploadMetadata uploads the template metadata to storage.
func UploadMetadata(ctx context.Context, persistence storage.StorageProvider, t Template) error {
	ctx, span := tracer.Start(ctx, "upload-metadata")
	defer span.End()

	templateFiles := storage.TemplateFiles{BuildID: t.Template.BuildID}
	metadataPath := templateFiles.StorageMetadataPath()

	object, err := persistence.OpenBlob(ctx, metadataPath, storage.MetadataObjectType)
	if err != nil {
		return fmt.Errorf("failed to open metadata object: %w", err)
	}

	metaBytes, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("failed to serialize metadata: %w", err)
	}

	err = object.Put(ctx, metaBytes)
	if err != nil {
		return fmt.Errorf("failed to write metadata: %w", err)
	}

	return nil
}
