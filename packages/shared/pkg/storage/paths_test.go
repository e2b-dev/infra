package storage

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSeekableKindFromPath(t *testing.T) {
	t.Parallel()

	p := Paths{BuildID: "build"}

	cases := []struct {
		name string
		path string
		want SeekableObjectType
		ct   CompressionType
	}{
		{"memfile", p.DataFile(MemfileName, CompressionNone), MemfileObjectType, CompressionNone},
		{"memfile zstd", p.DataFile(MemfileName, CompressionZstd), MemfileObjectType, CompressionZstd},
		{"memfile lz4", p.DataFile(MemfileName, CompressionLZ4), MemfileObjectType, CompressionLZ4},
		{"rootfs", p.DataFile(RootfsName, CompressionNone), RootFSObjectType, CompressionNone},
		{"rootfs zstd", p.DataFile(RootfsName, CompressionZstd), RootFSObjectType, CompressionZstd},
		{"snapfile is not seekable", p.DataFile(SnapfileName, CompressionNone), UnknownSeekableObjectType, CompressionNone},
		{"header is not a data file", p.HeaderFile(MemfileName), UnknownSeekableObjectType, CompressionNone},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			kind, ct := seekableObjectType(c.path)
			require.Equal(t, c.want, kind)
			require.Equal(t, c.ct, ct)
		})
	}
}
