package header

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

func TestIsZero(t *testing.T) {
	t.Parallel()

	assert.True(t, IsZero(make([]byte, RootfsBlockSize)))
	assert.False(t, IsZero([]byte{0, 1, 0}), "middle byte is sampled")

	// Non-zero byte buried where the 3-sample short-circuit cannot see it
	// must still be caught by the SIMD fallback.
	buf := make([]byte, RootfsBlockSize)
	buf[100] = 0xFF
	assert.False(t, IsZero(buf))

	buf = make([]byte, RootfsBlockSize)
	buf[RootfsBlockSize-2] = 0xFF
	assert.False(t, IsZero(buf), "non-zero just before the last sampled byte")
}

// newDiffHeader must carry forward ancestors the source knows about and
// backfill the uncompressed sentinel for ones it lacks, so a legacy ancestor
// resolves via GetBuildFrameData (no doomed proactive header load) while the
// still-unuploaded self build stays absent.
func TestNewDiffHeaderBackfillsAncestors(t *testing.T) {
	t.Parallel()

	const bs = 4096
	self := uuid.New()
	knownAncestor := uuid.New()
	legacyAncestor := uuid.New()

	mapping := []BuildMap{
		{Offset: 0, Length: bs, BuildId: self, BuildStorageOffset: 0},
		{Offset: bs, Length: bs, BuildId: knownAncestor, BuildStorageOffset: 0},
		{Offset: 2 * bs, Length: bs, BuildId: legacyAncestor, BuildStorageOffset: 0},
		{Offset: 3 * bs, Length: bs, BuildId: uuid.Nil, BuildStorageOffset: 0},
	}

	// Parent header carries only knownAncestor; legacyAncestor predates per-build
	// headers and is absent from the source.
	sourceBuilds := map[uuid.UUID]BuildData{knownAncestor: {Size: 123}}

	meta := &Metadata{Version: MetadataVersionV4, BlockSize: bs, Size: 4 * bs, BuildId: self, BaseBuildId: legacyAncestor}
	h, err := newDiffHeader(meta, mapping, sourceBuilds)
	require.NoError(t, err)

	// Self is excluded: its data isn't uploaded yet.
	_, hasSelf := h.Builds[self]
	require.False(t, hasSelf)

	// Known ancestor carried forward verbatim (not clobbered by the sentinel).
	require.Equal(t, BuildData{Size: 123}, h.Builds[knownAncestor])

	// Legacy ancestor gets the uncompressed sentinel, so GetBuildFrameData
	// returns a non-nil table and the read path never proactively refreshes it.
	bd, hasLegacy := h.Builds[legacyAncestor]
	require.True(t, hasLegacy)
	require.Equal(t, BuildData{}, bd)
	require.Equal(t, storage.UncompressedFrameTable, h.GetBuildFrameData(legacyAncestor))

	// Empty (uuid.Nil) regions don't create entries.
	_, hasNil := h.Builds[uuid.Nil]
	require.False(t, hasNil)

	require.True(t, h.IncompletePendingUpload)
}
