package scheduling_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/scheduling"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func hdr(buildID, baseID uuid.UUID) *header.Header {
	return &header.Header{
		Metadata: &header.Metadata{
			BuildId:     buildID,
			BaseBuildId: baseID,
			BlockSize:   4096,
		},
	}
}

func TestFromHeaders(t *testing.T) {
	t.Parallel()

	buildID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	memBase := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	rootBase := uuid.MustParse("33333333-3333-3333-3333-333333333333")

	memHeader := hdr(buildID, memBase)
	rootHeader := hdr(buildID, rootBase)

	t.Run("filesystem-only: nil memfile header yields rootfs-only metadata", func(t *testing.T) {
		t.Parallel()
		md := scheduling.FromHeaders(buildID, nil, rootHeader, 0)

		require.NotNil(t, md)
		assert.Equal(t, buildID.String(), md.GetBuildId())
		assert.Equal(t, rootBase.String(), md.GetRootfsBaseBuildId())
		assert.NotEmpty(t, md.GetRootfsBuildIds())

		// Memfile fields must be empty for a filesystem-only snapshot.
		assert.Empty(t, md.GetMemfileBaseBuildId())
		assert.Empty(t, md.GetMemfileBuildIds())
		assert.Empty(t, md.GetMemfileBuildBytes())
		assert.Zero(t, md.GetMemfileDroppedBuilds())
	})

	t.Run("memfile header with nil Metadata is treated as absent", func(t *testing.T) {
		t.Parallel()
		md := scheduling.FromHeaders(buildID, &header.Header{}, rootHeader, 0)

		require.NotNil(t, md)
		assert.NotEmpty(t, md.GetRootfsBuildIds())
		assert.Empty(t, md.GetMemfileBaseBuildId())
		assert.Empty(t, md.GetMemfileBuildIds())
	})

	t.Run("nil rootfs header yields nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, scheduling.FromHeaders(buildID, memHeader, nil, 0))
	})

	t.Run("rootfs header with nil Metadata yields nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, scheduling.FromHeaders(buildID, memHeader, &header.Header{}, 0))
	})

	t.Run("both headers present populate memfile and rootfs", func(t *testing.T) {
		t.Parallel()
		md := scheduling.FromHeaders(buildID, memHeader, rootHeader, 1024)

		require.NotNil(t, md)
		assert.Equal(t, memBase.String(), md.GetMemfileBaseBuildId())
		assert.Equal(t, rootBase.String(), md.GetRootfsBaseBuildId())
		assert.NotEmpty(t, md.GetMemfileBuildIds())
		assert.NotEmpty(t, md.GetRootfsBuildIds())
	})
}
