//go:build linux

package sandbox

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeDiffSource is a block.DiffSource that is not a *block.DedupedMemfdCache,
// so buildProvisionalMemfile must decline to produce a provisional header.
type fakeDiffSource struct{}

func (fakeDiffSource) Close() error                            { return nil }
func (fakeDiffSource) ReadAt([]byte, int64) (int, error)       { return 0, nil }
func (fakeDiffSource) Slice(int64, int64) ([]byte, error)      { return nil, nil }
func (fakeDiffSource) Size() (int64, error)                    { return 0, nil }
func (fakeDiffSource) FileSize(context.Context) (int64, error) { return 0, nil }
func (fakeDiffSource) BlockSize() int64                        { return 0 }
func (fakeDiffSource) Path(context.Context) (string, error)    { return "", nil }

// buildProvisionalMemfile must fall back (return nil, nil, nil) when it can't
// apply, so the caller keeps using the deduped header: a non-memfd-dedup source,
// and a disabled flag.
func TestBuildProvisionalMemfile_FallsBack(t *testing.T) {
	t.Parallel()

	// Source is not a *block.DedupedMemfdCache → no provisional serving.
	h, d, swapDone := buildProvisionalMemfile(t.Context(), fakeDiffSource{}, true, nil, nil, nil)
	require.Nil(t, h)
	require.Nil(t, d)
	require.Nil(t, swapDone)

	// Disabled flag → no provisional serving.
	h, d, swapDone = buildProvisionalMemfile(t.Context(), fakeDiffSource{}, false, nil, nil, nil)
	require.Nil(t, h)
	require.Nil(t, d)
	require.Nil(t, swapDone)
}
