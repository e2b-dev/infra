package server

import (
	"errors"
	"fmt"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// A memfd mmap out of hugepages is capacity that frees up, so it is refused
// for placement to retry: a killed sandbox keeps its pages until its
// Firecracker exits, after Delete returned. Any other error is left to the caller.
func TestMemoryRefusal(t *testing.T) {
	t.Parallel()

	// The chain a create right after a kill returns.
	uffdExit := fmt.Errorf("uffd process exited: %w", errors.Join(fmt.Errorf("failed to wrap memfd: %w", fmt.Errorf("mmap memfd: %w", unix.ENOMEM))))
	load := &url.Error{Op: "Put", URL: "http://localhost/snapshot/load", Err: uffdExit}
	err := memoryRefusal(fmt.Errorf("failed to start FC: %w", fmt.Errorf("error loading snapshot: %w", load)))
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.ResourceExhausted, st.Code())
	assert.Contains(t, st.Message(), "mmap memfd: cannot allocate memory")

	assert.NoError(t, memoryRefusal(errors.New("mmap memfd: cannot allocate memory")), "only the errno counts, not the text")
	assert.NoError(t, memoryRefusal(errors.New("envd did not start")))
}
