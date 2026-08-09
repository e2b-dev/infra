//go:build linux

package finalize

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// packCertBundleCmd ships verbatim to the guest's sh; a parse error fails every build.
func TestPackCertBundleCmdIsValidSh(t *testing.T) {
	t.Parallel()

	shPath, err := exec.LookPath("sh")
	require.NoError(t, err, "sh is required to parse-check the guest script")

	script := filepath.Join(t.TempDir(), "pack-cert-bundle.sh")
	require.NoError(t, os.WriteFile(script, []byte(packCertBundleCmd), 0o600))

	out, err := exec.Command(shPath, "-n", script).CombinedOutput()
	require.NoErrorf(t, err, "packCertBundleCmd is not valid sh:\n%s", out)
}
