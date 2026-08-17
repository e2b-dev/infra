//go:build linux

package backend

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBackend_AllKnownTypes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	cases := []struct {
		typ      string
		wantType string
	}{
		{"local", "local"},
		{"juicefs", "juicefs"},
		{"cephfs", "cephfs"},
		{"ceph", "cephfs"},
		{"glusterfs", "glusterfs"},
		{"seaweedfs", "seaweedfs"},
		{"beegfs", "beegfs"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.typ, func(t *testing.T) {
			t.Parallel()

			b, err := NewBackend(tc.typ, root)
			require.NoError(t, err)
			require.NotNil(t, b)
			assert.Equal(t, tc.wantType, b.Type())
		})
	}
}

func TestNewBackend_UnknownType(t *testing.T) {
	t.Parallel()

	_, err := NewBackend("nfs", t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown volume backend type")
	assert.Contains(t, err.Error(), "nfs")
}

func TestNewBackend_EmptyType(t *testing.T) {
	t.Parallel()

	_, err := NewBackend("", t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown volume backend type")
}
