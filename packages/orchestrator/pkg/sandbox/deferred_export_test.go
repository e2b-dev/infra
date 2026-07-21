//go:build linux

package sandbox

import (
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// TestRunDeferredRootfsExport verifies the background lifecycle of the deferred
// rootfs export: the frozen (ejected) cache is reflinked into the deferred diff,
// the diff resolves with the sealed bytes, and the cache is closed — with no
// overlay/provider interaction (the sandbox is already stopped).
func TestRunDeferredRootfsExport(t *testing.T) {
	t.Parallel()

	blockSize := int64(header.PageSize)
	numBlocks := int64(3)
	size := blockSize * numBlocks

	// A standalone frozen cache with block 1 dirtied (stands in for the ejected
	// COW cache after the sandbox is stopped).
	sealCache, err := block.NewCache(size, blockSize, t.TempDir()+"/ejected", false)
	require.NoError(t, err)
	blockData := make([]byte, blockSize)
	for i := range blockData {
		blockData[i] = 0x5C
	}
	_, err = sealCache.WriteAt(blockData, blockSize)
	require.NoError(t, err)

	s := &Sandbox{
		Resources: &Resources{},
		config:    cfg.BuilderConfig{DefaultCacheDir: t.TempDir()},
	}

	buildID := uuid.New()
	diffPromise := utils.NewSetOnce[build.Diff]()

	s.runDeferredRootfsExport(t.Context(), sealCache, buildID, blockSize, diffPromise)

	diff, err := diffPromise.Result()
	require.NoError(t, err)
	path, err := diff.CachePath(t.Context())
	require.NoError(t, err)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, blockData, got, "deferred diff must contain the dirtied block")
	require.NoError(t, diff.Close())
}
