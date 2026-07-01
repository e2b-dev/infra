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

// TestBlobType pins blobType to its fixed vocabulary: the known whole-object
// blobs (header, snapfile, metadata) and "other" for everything else. The
// seekable data files memfile/rootfs.ext4 are NOT blobs and must never be
// returned, and no per-hash/per-build path may leak — the cardinality blowup
// from #3063.
func TestBlobType(t *testing.T) {
	t.Parallel()

	p := Paths{BuildID: "11111111-1111-1111-1111-111111111111"}
	const hash = "deadbeefcafef00ddeadbeefcafef00ddeadbeefcafef00ddeadbeefcafef00d"

	cases := []struct {
		name string
		path string
		want string
	}{
		{"memfile header", p.MemfileHeader(), blobTypeHeader},
		{"rootfs header", p.RootfsHeader(), blobTypeHeader},
		{"snapfile", p.Snapfile(), blobTypeSnapfile},
		{"metadata", p.Metadata(), blobTypeMetadata},
		// Not known blobs — bounded "other", never the raw name or hash.
		{"memfile data file is not a blob", p.Memfile(), blobTypeOther},
		{"rootfs data file is not a blob", p.RootfsCompressed(CompressionZstd), blobTypeOther},
		{"layer files keyed by hash", "scope-abc/files/" + hash + ".tar", blobTypeOther},
		{"unknown collapses to other", p.BuildID + "/something-else", blobTypeOther},
		{"bare hash never leaks", hash, blobTypeOther},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, c.want, blobType(c.path))
		})
	}
}
