//go:build linux

package oci

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/moby/go-archive"
	"github.com/moby/go-archive/chrootarchive"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// untarLayerToDir runs the same unpack call used by createExport in oci.go.
func untarLayerToDir(t *testing.T, layer *bytes.Buffer) string {
	t.Helper()

	layerPath := t.TempDir()
	err := chrootarchive.UntarUncompressed(layer, layerPath, &archive.TarOptions{
		WhiteoutFormat: archive.OverlayWhiteoutFormat,
	})
	require.NoError(t, err)

	return layerPath
}

func requireRoot(t *testing.T) {
	t.Helper()

	if os.Geteuid() != 0 {
		t.Skip("chrootarchive requires root (unshare + chroot)")
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
