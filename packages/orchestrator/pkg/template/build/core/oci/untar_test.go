//go:build linux

package oci

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/moby/go-archive"
	"github.com/moby/go-archive/chrootarchive"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// untarLayer runs the same unpack call used by createExport in oci.go.
func untarLayer(t *testing.T, layer *bytes.Buffer) (string, error) {
	t.Helper()

	layerPath := t.TempDir()
	err := chrootarchive.UntarUncompressed(layer, layerPath, &archive.TarOptions{
		WhiteoutFormat: archive.OverlayWhiteoutFormat,
	})

	return layerPath, err
}

// untarLayerToDir is untarLayer for layers that must unpack successfully.
func untarLayerToDir(t *testing.T, layer *bytes.Buffer) string {
	t.Helper()

	layerPath, err := untarLayer(t, layer)
	require.NoError(t, err)

	return layerPath
}

func requireRoot(t *testing.T) {
	t.Helper()

	if os.Geteuid() != 0 {
		t.Skip("chrootarchive requires root (unshare + chroot)")
	}

	// euid 0 alone is not enough: rootful containers may lack CAP_SYS_ADMIN,
	// making unshare(CLONE_NEWNS) fail with EPERM. Probe the actual
	// capability the same way chrootarchive uses it: on a locked OS thread
	// that is discarded afterwards (goroutine exits without UnlockOSThread,
	// so the runtime destroys the thread and the unshared state with it).
	errCh := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		errCh <- unix.Unshare(unix.CLONE_FS | unix.CLONE_NEWNS)
	}()
	if err := <-errCh; err != nil {
		t.Skipf("chrootarchive requires unshare(CLONE_FS|CLONE_NEWNS): %v", err)
	}
}

// TestUntarLayerWithEscapingRelativeSymlink reproduces layers produced by
// `nix store optimise`: /nix/store/.links contains hard-linked symlinks whose
// relative targets (e.g. ../../../../../etc/environment) lexically escape the
// layer directory but are clamped at / by the kernel at runtime. A plain
// archive.Untar rejects such layers with `invalid symlink ...`; the
// chroot-based unpack must accept them and preserve the target verbatim.
func TestUntarLayerWithEscapingRelativeSymlink(t *testing.T) {
	t.Parallel()
	requireRoot(t)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	for _, dir := range []string{"nix", "nix/store", "nix/store/.links", "etc"} {
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name:     dir + "/",
			Typeflag: tar.TypeDir,
			Mode:     0o755,
		}))
	}

	fileContent := []byte("PATH=/usr/bin\n")
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "etc/environment",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     int64(len(fileContent)),
	}))
	_, err := tw.Write(fileContent)
	require.NoError(t, err)

	// The problematic entry: 5 levels up from a directory only 3 levels deep.
	escapingTarget := "../../../../../etc/environment"
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "nix/store/.links/0wf6h8q4wf3zxykipqy67z3r0x03jvsz",
		Typeflag: tar.TypeSymlink,
		Linkname: escapingTarget,
		Mode:     0o777,
	}))

	// A regular relative symlink that stays inside the layer.
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "nix/store/link-within",
		Typeflag: tar.TypeSymlink,
		Linkname: "../../etc/environment",
		Mode:     0o777,
	}))

	require.NoError(t, tw.Close())

	layerPath := untarLayerToDir(t, &buf)

	target, err := os.Readlink(filepath.Join(layerPath, "nix/store/.links/0wf6h8q4wf3zxykipqy67z3r0x03jvsz"))
	require.NoError(t, err)
	assert.Equal(t, escapingTarget, target, "symlink target must be preserved verbatim")

	target, err = os.Readlink(filepath.Join(layerPath, "nix/store/link-within"))
	require.NoError(t, err)
	assert.Equal(t, "../../etc/environment", target)

	content, err := os.ReadFile(filepath.Join(layerPath, "etc/environment"))
	require.NoError(t, err)
	assert.Equal(t, fileContent, content)
}

// TestUntarLayerNestedWhiteoutsUnsupported documents that layers containing
// redundant nested whiteouts (.wh.foo + foo/.wh.bar, emitted by some builders
// for rm -rf-style deletes) are not supported: converting .wh.foo to overlay
// format creates a 0:0 char device at foo, so the nested mknod at foo/bar
// fails with ENOTDIR. Stock Docker rejects such images the same way (both the
// overlay2 graphdriver and the containerd snapshotter); only the
// containers/storage stack (podman/buildah) tolerates them.
func TestUntarLayerNestedWhiteoutsUnsupported(t *testing.T) {
	t.Parallel()
	requireRoot(t)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     ".wh.foo",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     0,
	}))
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "foo/.wh.bar",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     0,
	}))
	require.NoError(t, tw.Close())

	_, err := untarLayer(t, &buf)
	require.ErrorContains(t, err, "not a directory")
}

// TestUntarLayerBlocksTraversalThroughSymlink verifies the chroot-based
// unpack still prevents actual path traversal: a later tar entry whose path
// goes through a previously created symlink must not write outside the layer
// directory.
func TestUntarLayerBlocksTraversalThroughSymlink(t *testing.T) {
	t.Parallel()
	requireRoot(t)

	outside := t.TempDir()

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "escape",
		Typeflag: tar.TypeSymlink,
		Linkname: outside,
		Mode:     0o777,
	}))

	payload := []byte("pwned")
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "escape/pwned",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     int64(len(payload)),
	}))
	_, err := tw.Write(payload)
	require.NoError(t, err)
	require.NoError(t, tw.Close())

	layerPath := t.TempDir()
	// Whether unpack errors out or resolves the path inside the chroot, the
	// file must never appear outside the layer directory.
	_ = chrootarchive.UntarUncompressed(&buf, layerPath, &archive.TarOptions{
		WhiteoutFormat: archive.OverlayWhiteoutFormat,
	})

	_, err = os.Stat(filepath.Join(outside, "pwned"))
	assert.True(t, os.IsNotExist(err), "file must not be written outside the layer directory")
}
