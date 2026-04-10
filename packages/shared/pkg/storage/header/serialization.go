package header

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// SerializeHeader serializes a header, dispatching to the version-specific format.
//
// V3 (Version <= 3): [Metadata] [v3 mappings…]
// V4 (Version >= 4): [Metadata] [uint32 uncompressedSize] [LZ4( BuildFiles + v4 mappings + FrameTables )]
func SerializeHeader(h *Header) ([]byte, error) {
	if h.Metadata.Version <= 3 {
		return serializeV3(h.Metadata, h.Mapping)
	}

	return serializeV4(h.Metadata, h.BuildFiles, h.Mapping)
}

// DeserializeBytes auto-detects the header version and deserializes accordingly.
// See SerializeHeader for the binary layout.
func DeserializeBytes(data []byte) (*Header, error) {
	if len(data) < metadataSize {
		return nil, fmt.Errorf("header too short: %d bytes", len(data))
	}

	metadata, err := deserializeMetadata(data[:metadataSize])
	if err != nil {
		return nil, err
	}

	blockData := data[metadataSize:]

	if metadata.Version >= 4 {
		return deserializeV4(metadata, blockData)
	}

	return deserializeV3(metadata, blockData)
}

// LoadHeader fetches a serialized header from storage and deserializes it.
// Errors (including storage.ErrObjectNotExist) are returned as-is.
func LoadHeader(ctx context.Context, s storage.StorageProvider, path string) (*Header, error) {
	blob, err := s.OpenBlob(ctx, path, storage.MetadataObjectType)
	if err != nil {
		return nil, fmt.Errorf("open blob %s: %w", path, err)
	}

	data, err := storage.GetBlob(ctx, blob)
	if err != nil {
		return nil, err
	}

	return DeserializeBytes(data)
}

// StoreHeader serializes a header and uploads it to storage.
// Inverse of LoadHeader.
func StoreHeader(ctx context.Context, s storage.StorageProvider, path string, h *Header) ([]byte, error) {
	data, err := SerializeHeader(h)
	if err != nil {
		return nil, fmt.Errorf("serialize header: %w", err)
	}

	blob, err := s.OpenBlob(ctx, path, storage.MetadataObjectType)
	if err != nil {
		return nil, fmt.Errorf("open blob %s: %w", path, err)
	}

	return data, blob.Put(ctx, data)
}

// Deserialize reads a header from a storage Blob (legacy API).
func Deserialize(ctx context.Context, in storage.Blob) (*Header, error) {
	data, err := storage.GetBlob(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("failed to write to buffer: %w", err)
	}

	return DeserializeBytes(data)
}
