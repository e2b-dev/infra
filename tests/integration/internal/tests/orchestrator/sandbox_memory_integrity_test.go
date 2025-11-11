package orchestrator

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestSandboxMemoryIntegrity(t *testing.T) {
	t.Parallel()

	c := setup.GetAPIClient()

	t.Run("via tmpfs hash", func(t *testing.T) {
		t.Parallel()

		// Create a sandbox with auto-pause disabled
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))
		sbxId := sbx.SandboxID

		envdClient := setup.GetEnvdClient(t, t.Context())

		installCmd := `apt-get update && apt-get install -y time`
		err := utils.ExecCommandAsRoot(t, t.Context(), sbx, envdClient, "bash", "-c", installCmd)
		require.NoError(t, err)

		tmpfsFile := "/mnt/testfile"

		percentageOfFreeMemoryToUse := 80
		// Create a tmpfs with up to 80% of the remaining free RAM and fill it with random data
		// Disable swap to ensure we're testing pure RAM-based storage
		// Always use at least 64MB, but not more than 80% of free memory
		memCmd := fmt.Sprintf(`
TOTAL_MEM_MB=$(free -m | awk '/^Mem:/ {print $2}')
USED_MEM_MB=$(free -m | awk '/^Mem:/ {print $3}')
FREE_MEM_MB=$(free -m | awk '/^Mem:/ {print $7}')
# Calculate %d%% of free memory, rounding down
MEM_MB=$(( (FREE_MEM_MB * %d) / 100 ))
if [ "$MEM_MB" -lt 64 ]; then MEM_MB=64; fi
echo "Total memory: ${TOTAL_MEM_MB} MB"
echo "Used memory before tmpfs mount: ${USED_MEM_MB} MB"
echo "Free memory before tmpfs mount: ${FREE_MEM_MB} MB"
echo "Memory to use in integrity test (%d%% of free, min 64MB): ${MEM_MB} MB"
swapoff -a
mount -t tmpfs -o size=${MEM_MB}M tmpfs /mnt
/usr/bin/time -v dd if=/dev/urandom of="%s" bs=1M count=${MEM_MB}
USED_MEM_MB_AFTER=$(free -m | awk '/^Mem:/ {print $3}')
echo "Used memory after tmpfs mount and file fill: ${USED_MEM_MB_AFTER} MB"
`, percentageOfFreeMemoryToUse, percentageOfFreeMemoryToUse, percentageOfFreeMemoryToUse, tmpfsFile)

		_, err = utils.ExecCommandAsRootWithOutput(t, t.Context(), sbx, envdClient, "bash", "-c", memCmd)
		require.NoError(t, err)

		hashCmd := fmt.Sprintf(`sha256sum "%s" | awk '{print $1}'`, tmpfsFile)
		hashCmdOutput, err := utils.ExecCommandAsRootWithOutput(t, t.Context(), sbx, envdClient, "bash", "-c", hashCmd)
		require.NoError(t, err)
		hashCmdOutput = strings.TrimSpace(hashCmdOutput)
		require.NotEmpty(t, hashCmdOutput, "Failed to extract hash from command output")

		pauseIterations := 2

		for i := range pauseIterations {
			resp, err := c.PostSandboxesSandboxIDPauseWithResponse(t.Context(), sbxId, setup.WithAPIKey())
			require.NoError(t, err)
			require.Equal(t, http.StatusNoContent, resp.StatusCode())

			res, err := c.GetSandboxesSandboxIDWithResponse(t.Context(), sbxId, setup.WithAPIKey())
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, res.StatusCode())
			require.NotNil(t, res.JSON200)
			assert.Equal(t, api.Paused, res.JSON200.State)

			sbxResume, err := c.PostSandboxesSandboxIDResumeWithResponse(t.Context(), sbxId, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
			require.NoError(t, err)
			require.Equal(t, http.StatusCreated, sbxResume.StatusCode())
			require.NotNil(t, sbxResume.JSON201)
			assert.Equal(t, sbxResume.JSON201.SandboxID, sbxId)

			// Check the tmpfs hash and compare it with the original hash
			hashCmdOutputAfterResume, err := utils.ExecCommandAsRootWithOutput(t, t.Context(), sbx, envdClient, "bash", "-c", hashCmd)
			require.NoError(t, err)
			hashCmdOutputAfterResume = strings.TrimSpace(hashCmdOutputAfterResume)

			assert.Equal(t, hashCmdOutput, hashCmdOutputAfterResume, "Hash mismatch on iteration %d: memory integrity check failed", i)
		}
	})

	t.Run("via stress-ng verify", func(t *testing.T) {
		t.Parallel()

		// Create a sandbox with auto-pause disabled
		sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithAutoPause(false))

		envdClient := setup.GetEnvdClient(t, t.Context())

		installCmd := `apt-get update && apt-get install -y stress-ng time`
		err := utils.ExecCommandAsRoot(t, t.Context(), sbx, envdClient, "bash", "-c", installCmd)
		require.NoError(t, err)

		disableSwapCmd := `swapoff -a`
		err = utils.ExecCommandAsRoot(t, t.Context(), sbx, envdClient, "bash", "-c", disableSwapCmd)
		require.NoError(t, err)

		// get 80% size of the free memory and use it as the vm-bytes
		percentageOfFreeMemoryToUse := 80

		getFreeMemoryCmd := `free -m | awk '/^Mem:/ {print $7}'`
		freeMemoryStr, err := utils.ExecCommandAsRootWithOutput(t, t.Context(), sbx, envdClient, "bash", "-c", getFreeMemoryCmd)
		require.NoError(t, err)

		freeMemoryStr = strings.TrimSpace(freeMemoryStr)
		freeMemoryMB, err := strconv.ParseInt(freeMemoryStr, 10, 64)
		require.NoError(t, err, "Failed to parse free memory value: %s", freeMemoryStr)

		vmBytes := fmt.Sprintf("%dM", freeMemoryMB*int64(percentageOfFreeMemoryToUse)/100)

		// Run stress-ng verify
		verifyCmd := fmt.Sprintf(`/usr/bin/time -v stress-ng --vm 1 --vm-bytes %s --verify -v -t 5s`, vmBytes)
		verifyOutput, err := utils.ExecCommandAsRootWithOutput(t, t.Context(), sbx, envdClient, "bash", "-c", verifyCmd)
		require.NoError(t, err)

		// Verify stress-ng completed successfully
		assert.Contains(t, verifyOutput, "successful run completed", "stress-ng did not complete successfully")
		assert.Contains(t, verifyOutput, "metrics-check: all stressor metrics validated and sane", "stress-ng metrics check failed")
	})
}
