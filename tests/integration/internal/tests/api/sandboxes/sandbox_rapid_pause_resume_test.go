package sandboxes

import (
	"context"
	"crypto/sha256"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

// TestSandboxRapidSnapshotForkChain builds a tree of snapshots in rapid
// succession, exercising the multi-layer upload coordination:
//
//	A
//	├── B ── D
//	└── C
//
// Each child snapshot is created (and a sandbox forked from it) before the
// parent's upload has finalized. The verifier reads each build's V4 header
// directly from object storage and checks (a) ancestor lineage in the Builds
// map, and (b) self's data file checksum against BuildData.Checksum. If the
// inter-uploader sync was wrong, ancestors would be missing or self's data
// would not match its recorded checksum.
func TestSandboxRapidSnapshotForkChain(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()
	ctx := t.Context()

	rootSbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))

	snapA := createSnapshotTemplateWithCleanup(t, c, rootSbx.SandboxID, nil)
	buildA := defaultTagBuildID(t, ctx, c, snapA.SnapshotID)

	sbxB := utils.SetupSandboxWithCleanup(t, c,
		utils.WithTemplateID(snapA.SnapshotID),
		utils.WithAutoPause(false),
	)
	snapB := createSnapshotTemplateWithCleanup(t, c, sbxB.SandboxID, nil)
	buildB := defaultTagBuildID(t, ctx, c, snapB.SnapshotID)

	sbxC := utils.SetupSandboxWithCleanup(t, c,
		utils.WithTemplateID(snapA.SnapshotID),
		utils.WithAutoPause(false),
	)
	snapC := createSnapshotTemplateWithCleanup(t, c, sbxC.SandboxID, nil)
	buildC := defaultTagBuildID(t, ctx, c, snapC.SnapshotID)

	sbxD := utils.SetupSandboxWithCleanup(t, c,
		utils.WithTemplateID(snapB.SnapshotID),
		utils.WithAutoPause(false),
	)
	snapD := createSnapshotTemplateWithCleanup(t, c, sbxD.SandboxID, nil)
	buildD := defaultTagBuildID(t, ctx, c, snapD.SnapshotID)

	chain := []chainNode{
		{name: "A", templateID: snapA.SnapshotID, buildID: buildA, parent: ""},
		{name: "B", templateID: snapB.SnapshotID, buildID: buildB, parent: buildA},
		{name: "C", templateID: snapC.SnapshotID, buildID: buildC, parent: buildA},
		{name: "D", templateID: snapD.SnapshotID, buildID: buildD, parent: buildB},
	}

	verifyChainOnStorage(t, ctx, chain)
}

type chainNode struct {
	name       string
	templateID string
	buildID    string
	parent     string // empty for root
}

// verifyChainOnStorage loads each build's V4 memfile/rootfs headers directly
// from the configured storage backend and asserts (a) ancestor lineage in
// the Builds map and (b) self's data file matches its recorded checksum.
//
// Skipped when TEMPLATE_BUCKET_NAME / STORAGE_PROVIDER aren't set.
func verifyChainOnStorage(t *testing.T, ctx context.Context, chain []chainNode) {
	t.Helper()

	if os.Getenv("TEMPLATE_BUCKET_NAME") == "" && !storage.IsLocal() {
		t.Log("storage env not configured (TEMPLATE_BUCKET_NAME / STORAGE_PROVIDER); skipping direct storage verification")

		return
	}

	persistence, err := storage.GetStorageProvider(ctx, storage.TemplateStorageConfig)
	require.NoError(t, err, "build storage provider")

	ancestors := make(map[string][]string, len(chain))
	for _, node := range chain {
		var chainAncestors []string
		if node.parent != "" {
			chainAncestors = append(chainAncestors, ancestors[node.parent]...)
			chainAncestors = append(chainAncestors, node.parent)
		}
		ancestors[node.buildID] = chainAncestors

		paths := storage.Paths{BuildID: node.buildID}
		verifyHeader(t, ctx, persistence, node, paths, storage.MemfileName, paths.MemfileHeader(), storage.MemfileObjectType, chainAncestors)
		verifyHeader(t, ctx, persistence, node, paths, storage.RootfsName, paths.RootfsHeader(), storage.RootFSObjectType, chainAncestors)
	}
}

func verifyHeader(t *testing.T, ctx context.Context, persistence storage.StorageProvider, node chainNode, paths storage.Paths, fileName, headerPath string, objType storage.SeekableObjectType, ancestors []string) {
	t.Helper()

	h := loadHeaderWithPolling(t, ctx, persistence, headerPath, node.name, fileName)
	require.NotNilf(t, h.Builds, "%s/%s: V4 header should carry Builds map", node.name, fileName)

	selfUUID := uuid.MustParse(node.buildID)
	bd, ok := h.Builds[selfUUID]
	require.Truef(t, ok, "%s/%s: Builds map missing self entry %s", node.name, fileName, node.buildID)

	for _, ancestor := range ancestors {
		ancUUID := uuid.MustParse(ancestor)
		_, ok := h.Builds[ancUUID]
		assert.Truef(t, ok, "%s/%s: Builds map missing ancestor %s — child finalized before parent's SwapHeader", node.name, fileName, ancestor)
	}

	verifyChecksum(t, ctx, persistence, node, paths, fileName, objType, bd)
}

// verifyChecksum streams self's data file through SHA-256 and compares to
// BuildData.Checksum. For unchanged files (empty diff) the entry has zero
// values and this is a no-op.
func verifyChecksum(t *testing.T, ctx context.Context, persistence storage.StorageProvider, node chainNode, paths storage.Paths, fileName string, objType storage.SeekableObjectType, bd header.BuildData) {
	t.Helper()

	if bd.Size == 0 {
		return // no data uploaded for this file in this layer
	}

	dataPath := paths.DataFile(fileName, bd.FrameData.CompressionType())

	obj, err := persistence.OpenSeekable(ctx, dataPath, objType)
	require.NoErrorf(t, err, "%s/%s: open data file %s", node.name, fileName, dataPath)

	rc, err := obj.OpenRangeReader(ctx, 0, bd.Size, bd.FrameData)
	require.NoErrorf(t, err, "%s/%s: open range reader", node.name, fileName)
	defer rc.Close()

	hasher := sha256.New()
	n, err := io.Copy(hasher, rc)
	require.NoErrorf(t, err, "%s/%s: stream data through hasher", node.name, fileName)
	require.Equalf(t, bd.Size, n, "%s/%s: streamed bytes (%d) differ from BuildData.Size (%d)", node.name, fileName, n, bd.Size)

	var got [32]byte
	copy(got[:], hasher.Sum(nil))
	require.Equalf(t, bd.Checksum, got, "%s/%s: data SHA-256 does not match BuildData.Checksum — upload corrupted or checksum stale", node.name, fileName)
}

// loadHeaderWithPolling waits for the V4 header to appear in object storage —
// snapshot uploads are async, so the header may not be present immediately
// after the snapshot endpoint returns 201.
func loadHeaderWithPolling(t *testing.T, ctx context.Context, persistence storage.StorageProvider, path, name, fileLabel string) *header.Header {
	t.Helper()

	var h *header.Header
	require.Eventually(t, func() bool {
		var err error
		h, err = header.LoadHeader(ctx, persistence, path)

		return err == nil && h != nil
	}, 2*time.Minute, 500*time.Millisecond, "%s/%s: %s never appeared in storage", name, fileLabel, path)

	return h
}

func defaultTagBuildID(t *testing.T, ctx context.Context, c *api.ClientWithResponses, snapshotID string) string {
	t.Helper()

	tagsResp, err := c.GetTemplatesTemplateIDTagsWithResponse(ctx, snapshotID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, tagsResp.StatusCode())
	require.NotNil(t, tagsResp.JSON200)
	require.NotEmpty(t, *tagsResp.JSON200)

	return findDefaultTagBuildID(t, *tagsResp.JSON200).String()
}
