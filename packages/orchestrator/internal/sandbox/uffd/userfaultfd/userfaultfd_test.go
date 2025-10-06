package userfaultfd

import (
	"bytes"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type pageTest struct {
	name            string
	pagesize        uint64
	numberOfPages   uint64
	operationOffset uint64
}

func TestUffdMissing(t *testing.T) {
	tests := []pageTest{
		{
			name:            "standard 4k page, operation at start",
			pagesize:        header.PageSize,
			numberOfPages:   32,
			operationOffset: 0,
		},
		{
			name:            "standard 4k page, operation at middle",
			pagesize:        header.PageSize,
			numberOfPages:   32,
			operationOffset: 16 * header.PageSize,
		},
		{
			name:            "standard 4k page, operation at last page",
			pagesize:        header.PageSize,
			numberOfPages:   32,
			operationOffset: 31 * header.PageSize,
		},
		{
			name:            "hugepage, operation at start",
			pagesize:        header.HugepageSize,
			numberOfPages:   8,
			operationOffset: 0,
		},
		{
			name:            "hugepage, operation at middle",
			pagesize:        header.HugepageSize,
			numberOfPages:   8,
			operationOffset: 4 * header.HugepageSize,
		},
		{
			name:            "hugepage, operation at last page",
			pagesize:        header.HugepageSize,
			numberOfPages:   8,
			operationOffset: 7 * header.HugepageSize,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, size := testutils.RandomPages(tt.pagesize, tt.numberOfPages)

			uffd, err := newUserfaultfd(syscall.O_CLOEXEC | syscall.O_NONBLOCK)
			require.NoError(t, err)

			t.Cleanup(func() {
				uffd.Close()
			})

			err = uffd.configureApi(tt.pagesize)
			require.NoError(t, err)

			memoryArea, memoryStart, unmap, err := testutils.NewPageMmap(size, tt.pagesize)
			require.NoError(t, err)

			t.Cleanup(func() {
				unmap()
			})

			err = uffd.Register(memoryStart, size, UFFDIO_REGISTER_MODE_MISSING)
			require.NoError(t, err)

			m := testutils.NewContiguousMap(memoryStart, size, tt.pagesize)

			fdExit, err := fdexit.New()
			require.NoError(t, err)

			t.Cleanup(func() {
				fdExit.SignalExit()
				fdExit.Close()
			})

			exitUffd := make(chan struct{}, 1)

			go func() {
				err := uffd.Serve(t.Context(), m, data, fdExit, zap.L())
				assert.NoError(t, err)

				exitUffd <- struct{}{}
			}()

			d, err := data.Slice(t.Context(), int64(tt.operationOffset), int64(tt.pagesize))
			require.NoError(t, err)

			if !bytes.Equal(memoryArea[tt.operationOffset:tt.operationOffset+tt.pagesize], d) {
				idx, want, got := testutils.DiffByte(memoryArea[tt.operationOffset:tt.operationOffset+tt.pagesize], d)
				t.Fatalf("content mismatch: want %q, got %q at index %d", want, got, idx)
			}

			assert.Equal(t, m.Map(), map[uint64]struct{}{tt.operationOffset: {}})

			signalExitErr := fdExit.SignalExit()
			require.NoError(t, signalExitErr)

			select {
			case <-exitUffd:
			case <-t.Context().Done():
				t.Fatal("context done before exit", t.Context().Err())
			}
		})
	}
}
