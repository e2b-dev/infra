//go:build linux

package template

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	blockmocks "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/mocks"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// durableMemfileDevice is a ReadonlyDevice that also exposes DurableHeaderNow,
// like the real *Storage. Header() stands in for the live (provisional) header;
// DurableHeaderNow returns the deduped header scheduling metadata must use, and
// its ready flag models whether the deduped header has resolved yet.
type durableMemfileDevice struct {
	*blockmocks.MockReadonlyDevice

	durable *header.Header
	ready   bool
}

func (d durableMemfileDevice) DurableHeaderNow() (*header.Header, bool) {
	return d.durable, d.ready
}

func schedulingTemplate(t *testing.T, mem block.ReadonlyDevice, rootfsBase uuid.UUID) *storageTemplate {
	t.Helper()
	rootfsHdr, err := header.NewHeader(&header.Metadata{Version: 3, BlockSize: 4096, Size: 4096, BaseBuildId: rootfsBase}, nil)
	require.NoError(t, err)
	rootfsDev := blockmocks.NewMockReadonlyDevice(t)
	rootfsDev.EXPECT().Header().Return(rootfsHdr)

	tmpl := &storageTemplate{
		memfile: utils.NewSetOnce[block.ReadonlyDevice](),
		rootfs:  utils.NewSetOnce[block.ReadonlyDevice](),
	}
	require.NoError(t, tmpl.rootfs.SetValue(rootfsDev))
	require.NoError(t, tmpl.memfile.SetValue(mem))

	return tmpl
}

// When the deduped header has resolved, SchedulingMetadata reports it (never the
// live/provisional header) — so memfile build ids reflect the real build id.
func TestStorageTemplate_SchedulingMetadataUsesDurableHeader(t *testing.T) {
	t.Parallel()

	dedupedBase := uuid.New()
	dedupedHdr, err := header.NewHeader(&header.Metadata{Version: 3, BlockSize: 4096, Size: 4096, BaseBuildId: dedupedBase}, nil)
	require.NoError(t, err)
	memMock := blockmocks.NewMockReadonlyDevice(t)
	memMock.EXPECT().Header().Return(nil).Maybe()
	memDev := durableMemfileDevice{MockReadonlyDevice: memMock, durable: dedupedHdr, ready: true}

	md := schedulingTemplate(t, memDev, uuid.New()).SchedulingMetadata(t.Context())
	require.NotNil(t, md)
	assert.Equal(t, dedupedBase.String(), md.GetMemfileBaseBuildId())
}

// While the deduped header is still pending (provisional window), SchedulingMetadata
// must NOT block and must NOT emit the provisional build id: it reports rootfs-only
// metadata (empty memfile base build id).
func TestStorageTemplate_SchedulingMetadataSkipsPendingMemfile(t *testing.T) {
	t.Parallel()

	rootfsBase := uuid.New()
	memMock := blockmocks.NewMockReadonlyDevice(t)
	memMock.EXPECT().Header().Return(nil).Maybe()
	memDev := durableMemfileDevice{MockReadonlyDevice: memMock, durable: nil, ready: false}

	md := schedulingTemplate(t, memDev, rootfsBase).SchedulingMetadata(t.Context())
	require.NotNil(t, md)
	assert.Empty(t, md.GetMemfileBaseBuildId())
	assert.Equal(t, rootfsBase.String(), md.GetRootfsBaseBuildId())
}
