package header

import (
	"context"
	"errors"
	"fmt"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// HeaderStoreStats reports byte sizes for a serialized header. Stored is what
// was written to storage; Uncompressed is the same payload sized as if the
// inner LZ4 block had not been compressed (V3 has no inner compression, so
// Stored == Uncompressed). Compression is the inner-block codec actually used.
type HeaderStoreStats struct {
	Stored       int64
	Uncompressed int64
	Compression  storage.CompressionType
}

// SerializeHeader serializes a header, dispatching to the version-specific format.
//
// V3 (Version <= 3): [Metadata] [v3 mappings…]
// V4 (Version >= 4): [Metadata] [uint8 flags] [uint32 uncompressedSize] [LZ4( Builds + v4 mappings )]
func SerializeHeader(h *Header) ([]byte, error) {
	data, _, err := serializeHeaderWithStats(h)

	return data, err
}

func serializeHeaderWithStats(h *Header) ([]byte, HeaderStoreStats, error) {
	if h.Metadata.Version <= 3 {
		data, err := serializeV3(h.Metadata, h.Mapping)
		if err != nil {
			return nil, HeaderStoreStats{}, err
		}

		return data, HeaderStoreStats{
			Stored:       int64(len(data)),
			Uncompressed: int64(len(data)),
			Compression:  storage.CompressionNone,
		}, nil
	}

	data, blockUncompressed, err := serializeV4(h.Metadata, h.Builds, h.Mapping, h.IncompletePendingUpload)
	if err != nil {
		return nil, HeaderStoreStats{}, err
	}

	overhead := int64(metadataSize + v4FlagsLen + v4SizePrefixLen)

	return data, HeaderStoreStats{
		Stored:       int64(len(data)),
		Uncompressed: overhead + blockUncompressed,
		Compression:  storage.CompressionLZ4,
	}, nil
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

// StoreHeader serializes a header, uploads it, and returns its size stats.
// Refuses to persist a header still flagged as in-flight — the upload pipeline
// must clear IncompletePendingUpload before reaching here.
func StoreHeader(ctx context.Context, s storage.StorageProvider, path string, h *Header) (HeaderStoreStats, error) {
	if h == nil {
		return HeaderStoreStats{}, errors.New("header is nil")
	}

	if h.IncompletePendingUpload {
		return HeaderStoreStats{}, fmt.Errorf("refusing to persist incomplete header for %s", path)
	}

	data, stats, err := serializeHeaderWithStats(h)
	if err != nil {
		return HeaderStoreStats{}, fmt.Errorf("serialize header: %w", err)
	}

	blob, err := s.OpenBlob(ctx, path, storage.MetadataObjectType)
	if err != nil {
		return HeaderStoreStats{}, fmt.Errorf("open blob %s: %w", path, err)
	}

	if err := blob.Put(ctx, data); err != nil {
		return HeaderStoreStats{}, fmt.Errorf("put blob %s: %w", path, err)
	}

	return stats, nil
}

// Deserialize reads a header from a storage Blob (legacy API).
func Deserialize(ctx context.Context, in storage.Blob) (*Header, error) {
	data, err := storage.GetBlob(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("failed to write to buffer: %w", err)
	}

	return DeserializeBytes(data)
}
