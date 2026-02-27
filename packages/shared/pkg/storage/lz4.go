package storage

import (
	"fmt"

	"github.com/pierrec/lz4/v4"
)

// MaxCompressedHeaderSize is the maximum allowed decompressed header size (64 MiB).
// Headers are typically a few hundred KiB; this is a safety bound.
const MaxCompressedHeaderSize = 64 << 20

// CompressLZ4 compresses data using LZ4 block compression.
// Returns an error if the data is incompressible (CompressBlock returns 0),
// since callers store the result as ".lz4" and DecompressLZ4 would fail on raw data.
func CompressLZ4(data []byte) ([]byte, error) {
	bound := lz4.CompressBlockBound(len(data))
	dst := make([]byte, bound)

	n, err := lz4.CompressBlock(data, dst, nil)
	if err != nil {
		return nil, fmt.Errorf("lz4 compress: %w", err)
	}

	if n == 0 {
		return nil, fmt.Errorf("lz4 compress: data is incompressible (%d bytes)", len(data))
	}

	return dst[:n], nil
}

// DecompressLZ4 decompresses LZ4-block-compressed data.
// maxSize is the maximum allowed decompressed size to prevent memory abuse.
func DecompressLZ4(data []byte, maxSize int) ([]byte, error) {
	dst := make([]byte, maxSize)

	n, err := lz4.UncompressBlock(data, dst)
	if err != nil {
		return nil, fmt.Errorf("lz4 decompress: %w", err)
	}

	return dst[:n], nil
}
