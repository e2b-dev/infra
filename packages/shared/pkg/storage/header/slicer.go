package header

import "context"

type Slicer interface {
	Slice(ctx context.Context, off, length int64) ([]byte, error)
}
