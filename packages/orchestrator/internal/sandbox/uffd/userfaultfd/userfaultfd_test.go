package userfaultfd

import (
	"bytes"
	"fmt"
	"reflect"
	"syscall"
	"testing"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/fdexit"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type pageTest struct {
	pagesize        uint64
	numberOfPages   uint64
	operationOffset uint64
}

func TestUffdMissing(t *testing.T) {
	tests := []pageTest{
		// Standard 4K page, operation at start
		{
			pagesize:        header.PageSize,
			numberOfPages:   32,
			operationOffset: 0,
		},
		// Standard 4K page, operation at middle
		{
			pagesize:        header.PageSize,
			numberOfPages:   32,
			operationOffset: 16 * header.PageSize,
		},
		// Standard 4K page, operation at last page
		{
			pagesize:        header.PageSize,
			numberOfPages:   32,
			operationOffset: 31 * header.PageSize,
		},
		// Hugepage, operation at start
		{
			pagesize:        header.HugepageSize,
			numberOfPages:   8,
			operationOffset: 0,
		},
		// Hugepage, operation at middle
		{
			pagesize:        header.HugepageSize,
			numberOfPages:   8,
			operationOffset: 4 * header.HugepageSize,
		},
		// Hugepage, operation at last page
		{
			pagesize:        header.HugepageSize,
			numberOfPages:   8,
			operationOffset: 7 * header.HugepageSize,
		},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("pagesize-%d-offset-%d", tt.pagesize, tt.operationOffset), func(t *testing.T) {
			data, size := testutils.RandomPages(tt.pagesize, tt.numberOfPages)

			uffd, err := newUserfaultfd(syscall.O_CLOEXEC | syscall.O_NONBLOCK)
			if err != nil {
				t.Fatal("failed to create userfaultfd", err)
			}
			t.Cleanup(func() {
				uffd.Close()
			})

			err = uffd.configureApi(tt.pagesize)
			if err != nil {
				t.Fatal("failed to configure uffd api", err)
			}

			memoryArea, memoryStart, unmap, err := testutils.NewPageMmap(size, tt.pagesize)
			if err != nil {
				t.Fatal("failed to create page mmap", err)
			}
			t.Cleanup(func() {
				unmap()
			})

			err = uffd.Register(memoryStart, size, UFFDIO_REGISTER_MODE_MISSING)
			if err != nil {
				t.Fatal("failed to register memory", err)
			}

			m := testutils.NewContiguousMap(memoryStart, size, tt.pagesize)

			fdExit, err := fdexit.New()
			if err != nil {
				t.Fatal("failed to create fd exit", err)
			}
			t.Cleanup(func() {
				fdExit.SignalExit()
				fdExit.Close()
			})

			exitUffd := make(chan struct{}, 1)

			go func() {
				err := uffd.Serve(t.Context(), m, data, fdExit, zap.L())
				if err != nil {
					fmt.Println("[TestUffdMissing] failed to serve uffd", err)
				}

				exitUffd <- struct{}{}
			}()

			d, err := data.Slice(t.Context(), int64(tt.operationOffset), int64(tt.pagesize))
			if err != nil {
				t.Fatal("cannot read content", err)
			}

			if !bytes.Equal(memoryArea[tt.operationOffset:tt.operationOffset+tt.pagesize], d) {
				idx, want, got := testutils.DiffByte(memoryArea[tt.operationOffset:tt.operationOffset+tt.pagesize], d)
				t.Fatalf("content mismatch: want %q, got %q at index %d", want, got, idx)
			}

			if !reflect.DeepEqual(m.Map(), map[uint64]struct{}{tt.operationOffset: {}}) {
				t.Fatalf("accessed mismatch: should be accessed %v, actually accessed %v", []uint64{tt.operationOffset}, m.Keys())
			}

			signalExitErr := fdExit.SignalExit()
			if signalExitErr != nil {
				t.Fatal("failed to signal exit", err)
			}

			select {
			case <-exitUffd:
			case <-t.Context().Done():
				t.Fatal("timeout waiting for uffd to exit")
			}
		})
	}
}
