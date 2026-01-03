package header

import (
	"bytes"
	"fmt"

	"go.opentelemetry.io/otel"
)

const (
	PageSize        = 2 << 11
	HugepageSize    = 2 << 20
	RootfsBlockSize = 2 << 11
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/shared/pkg/storage/header")

var (
	EmptyHugePage = make([]byte, HugepageSize)
	EmptyBlock    = make([]byte, RootfsBlockSize)
)

func IsEmptyBlock(block []byte, blockSize int64) (bool, error) {
	var emptyBuf []byte
	switch blockSize {
	case HugepageSize:
		emptyBuf = EmptyHugePage
	case RootfsBlockSize:
		emptyBuf = EmptyBlock
	default:
		return false, fmt.Errorf("block size not supported: %d", blockSize)
	}

	return bytes.Equal(block, emptyBuf), nil
}
