package header

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// SerializeHeader dispatches to V3 / V4 based on Metadata.Version.
// V3/V4 methods stay exported for callers that know the target version.
func (t *Header) SerializeHeader() ([]byte, error) {
	if t.Metadata.Version <= 3 {
		return t.SerializeV3()
	}

	return t.SerializeV4()
}

// DeserializeBytes auto-detects the header version and deserializes accordingly.
//
// V3 (Version <= 3): [Metadata] [v3 mappings…]
// V4 (Version >= 4): [Metadata] [uint32 uncompressedSize] [LZ4( Dependencies + v4 mappings )]
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

// Deserialize reads a header from a storage Blob (legacy API).
func Deserialize(ctx context.Context, in storage.Blob) (*Header, error) {
	data, err := storage.GetBlob(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("failed to write to buffer: %w", err)
	}

	return DeserializeBytes(data)
}
