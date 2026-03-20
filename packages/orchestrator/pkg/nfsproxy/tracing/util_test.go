package tracing

import (
	"io/fs"
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsUserError(t *testing.T) {
	t.Parallel()

	testCases := map[string]struct {
		err      error
		expected bool
	}{
		"os.ErrNotExist": {
			err:      os.ErrNotExist,
			expected: true,
		},
		"os.ErrExist": {
			err:      os.ErrExist,
			expected: true,
		},
		"fs.ErrNotExist": {
			err:      fs.ErrNotExist,
			expected: true,
		},
		"*fs.PathError(no such file)": {
			err:      &fs.PathError{Op: "open", Path: "no_such_file", Err: fs.ErrNotExist},
			expected: true,
		},
		"syscall.EEXIST": {
			err:      syscall.EEXIST,
			expected: true,
		},
		"syscall.ENOEXIST": {
			err:      syscall.ENOENT,
			expected: true,
		},
		"other error": {
			err:      syscall.EACCES,
			expected: false,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			actual := isUserError(tc.err)
			assert.Equal(t, tc.expected, actual)
		})
	}
}
