package userfaultfd

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestClassifyCopyResult covers UFFD audit findings #1 and #7. It pins the
// kernel partial-copy convention used by UFFDIO_COPY: when the syscall
// returns no errno, cpy.copy carries either the bytes copied or a negative
// -errno. Both EAGAIN-surfacing paths and any short positive copy must be
// classified as a soft errno so the caller drops the fault and lets the
// kernel redeliver instead of tearing the sandbox down.
//
// Mocking faultPage end-to-end would require an interface seam over Fd
// that this PR is too small to introduce; per the audit's "smallest
// pragmatic test" guidance we test the extracted classifier directly and
// rely on the existing cross-process matrix tests for integration coverage.
func TestClassifyCopyResult(t *testing.T) {
	t.Parallel()

	const fourKi = int64(4096)
	const twoMi = int64(2 * 1024 * 1024)

	tests := []struct {
		name        string
		bytesCopied int64
		pagesize    int64
		wantErr     error
		wantEAGAIN  bool
	}{
		{
			name:        "full 4k copy succeeds",
			bytesCopied: fourKi,
			pagesize:    fourKi,
			wantErr:     nil,
		},
		{
			name:        "kernel convention -EAGAIN surfaces as EAGAIN",
			bytesCopied: -int64(syscall.EAGAIN),
			pagesize:    fourKi,
			wantEAGAIN:  true,
		},
		{
			name:        "zero bytes copied surfaces as EAGAIN (matches Firecracker bytes_copied==0)",
			bytesCopied: 0,
			pagesize:    fourKi,
			wantEAGAIN:  true,
		},
		{
			name:        "partial positive copy on hugepage surfaces as EAGAIN",
			bytesCopied: fourKi,
			pagesize:    twoMi,
			wantEAGAIN:  true,
		},
		{
			name:        "kernel convention -EFAULT surfaces as EFAULT (still fatal upstream)",
			bytesCopied: -int64(syscall.EFAULT),
			pagesize:    fourKi,
			wantErr:     syscall.EFAULT,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := classifyCopyResult(tc.bytesCopied, tc.pagesize)
			if tc.wantEAGAIN {
				assert.ErrorIs(t, err, syscall.EAGAIN)

				return
			}
			assert.Equal(t, tc.wantErr, err)
		})
	}
}
