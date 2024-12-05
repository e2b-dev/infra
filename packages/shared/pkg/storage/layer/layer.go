package layer

import (
	"fmt"
	"io"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/layer/header"
)

type Layer struct {
	header *header.Header
	data   *Store
}

func NewFromStorage(
	headerData io.Reader,
	store *Store,
) (*Layer, error) {
	h, err := header.Deserialize(headerData)
	if err != nil {
		return nil, fmt.Errorf("failed to deserialize header: %w", err)
	}

	return &Layer{
		header: h,
		data:   store,
	}, nil
}

func (l *Layer) ReadAt(p []byte, off int64) (n int, err error) {
	blocks := ListBlocks(off, off+int64(len(p)), l.header.Metadata().BlockSize)

	for _, block := range blocks {
		mapping, err := l.header.GetMapping(block.Start)
		if err != nil {
			return 0, fmt.Errorf("failed to get mapping: %w", err)
		}

		source, err := l.data.Get(mapping.Source.String())
		if err != nil {
			return 0, fmt.Errorf("failed to get source: %w", err)
		}

		blockN, err := source.ReadAt(p[n:], int64(mapping.SourceStart))
		if err != nil {
			return 0, fmt.Errorf("failed to read from source: %w", err)
		}

		n += blockN
	}

	return n, nil
}
