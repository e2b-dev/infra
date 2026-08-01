//go:build linux

package template

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	blockmocks "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/mocks"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var errFailingMetafile = errors.New("metafile close failed")

// closableTemplate builds a storageTemplate whose devices are already resolved,
// mirroring a template that finished fetching and is now being evicted.
func closableTemplate(t *testing.T) (*storageTemplate, string, string) {
	t.Helper()

	dir := t.TempDir()
	snapPath := filepath.Join(dir, "snapfile")
	metaPath := filepath.Join(dir, "metafile")
	require.NoError(t, os.WriteFile(snapPath, []byte("snap"), 0o600))
	require.NoError(t, os.WriteFile(metaPath, []byte(`{"version":1}`), 0o600))

	memDev := blockmocks.NewMockReadonlyDevice(t)
	memDev.EXPECT().Close().Return(nil)
	rootfsDev := blockmocks.NewMockReadonlyDevice(t)
	rootfsDev.EXPECT().Close().Return(nil)

	tmpl := &storageTemplate{
		memfile:  utils.NewSetOnce[block.ReadonlyDevice](),
		rootfs:   utils.NewSetOnce[block.ReadonlyDevice](),
		snapfile: utils.NewSetOnce[File](),
		metafile: utils.NewSetOnce[File](),
	}
	require.NoError(t, tmpl.memfile.SetValue(memDev))
	require.NoError(t, tmpl.rootfs.SetValue(rootfsDev))
	require.NoError(t, tmpl.snapfile.SetValue(&storageFile{path: snapPath}))
	require.NoError(t, tmpl.metafile.SetValue(&storageFile{path: metaPath}))

	return tmpl, snapPath, metaPath
}

// Evicting a cached template must reclaim every file it owns. The metafile is
// owned by the template once AddSnapshot transfers it from the snapshot, so
// leaving it behind leaks one cache file per evicted template.
func TestStorageTemplate_CloseRemovesMetafile(t *testing.T) {
	t.Parallel()

	tmpl, snapPath, metaPath := closableTemplate(t)

	require.NoError(t, tmpl.Close(t.Context()))

	assert.NoFileExists(t, snapPath, "snapfile should be reclaimed on close")
	assert.NoFileExists(t, metaPath, "metafile should be reclaimed on close")
}

// failingFile is a File whose Close always fails.
type failingFile struct{ err error }

func (f failingFile) Path() string { return "/dev/null" }

func (f failingFile) Close() error { return f.err }

// A metafile that cannot be reclaimed must surface the failure rather than be
// dropped, so eviction problems stay visible.
func TestStorageTemplate_CloseReportsMetafileError(t *testing.T) {
	t.Parallel()

	tmpl, _, _ := closableTemplate(t)
	tmpl.metafile = utils.NewSetOnce[File]()
	require.NoError(t, tmpl.metafile.SetValue(failingFile{err: errFailingMetafile}))

	err := tmpl.Close(t.Context())

	require.ErrorIs(t, err, errFailingMetafile)
}

// A template can be evicted before its metafile has resolved (eviction racing
// the fetch). Close must not block waiting for it and must not report an error.
func TestStorageTemplate_CloseWithUnresolvedMetafile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	snapPath := filepath.Join(dir, "snapfile")
	require.NoError(t, os.WriteFile(snapPath, []byte("snap"), 0o600))

	memDev := blockmocks.NewMockReadonlyDevice(t)
	memDev.EXPECT().Close().Return(nil)
	rootfsDev := blockmocks.NewMockReadonlyDevice(t)
	rootfsDev.EXPECT().Close().Return(nil)

	tmpl := &storageTemplate{
		memfile:  utils.NewSetOnce[block.ReadonlyDevice](),
		rootfs:   utils.NewSetOnce[block.ReadonlyDevice](),
		snapfile: utils.NewSetOnce[File](),
		metafile: utils.NewSetOnce[File](),
	}
	require.NoError(t, tmpl.memfile.SetValue(memDev))
	require.NoError(t, tmpl.rootfs.SetValue(rootfsDev))
	require.NoError(t, tmpl.snapfile.SetValue(&storageFile{path: snapPath}))
	// metafile deliberately left unset.

	done := make(chan error, 1)
	go func() { done <- tmpl.Close(t.Context()) }()

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-t.Context().Done():
		t.Fatal("Close blocked on an unresolved metafile")
	}
}
