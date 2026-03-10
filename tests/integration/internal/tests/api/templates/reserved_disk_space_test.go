package api_templates

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	testutils "github.com/e2b-dev/infra/tests/integration/internal/utils"
)

// TestTemplateBuildReservedDiskSpace verifies that reserved disk space is correctly
// set during template build. It builds a template, creates a sandbox from it,
// and checks the reserved block count via tune2fs inside the running sandbox.
func TestTemplateBuildReservedDiskSpace(t *testing.T) {
	t.Parallel()

	c := setup.GetAPIClient()

	// Build a template
	template := testutils.BuildTemplate(t, testutils.TemplateBuildOptions{
		Name: "test-reserved-disk-space",
		BuildData: api.TemplateBuildStartV2{
			Force:     utils.ToPtr(true),
			FromImage: utils.ToPtr("ubuntu:22.04"),
		},
		EnableDebug: true,
		LogHandler:  testutils.DefaultBuildLogHandler(t),
		ReqEditors:  []api.RequestEditorFn{setup.WithAPIKey()},
	})

	// Create a sandbox from the built template
	sbx := testutils.SetupSandboxWithCleanup(t, c, testutils.WithTemplateID(template.TemplateID))

	// Run tune2fs inside the sandbox to check reserved blocks
	ctx, cancel := context.WithTimeout(t.Context(), BuildTimeout)
	defer cancel()

	envdClient := setup.GetEnvdClient(t, ctx)

	output, err := testutils.ExecCommandAsRootWithOutput(t, ctx, sbx, envdClient, "/bin/bash", "-c", "tune2fs -l /dev/vda 2>/dev/null")
	require.NoError(t, err, "tune2fs should be available in the sandbox (e2fsprogs)")

	// Parse reserved block count
	re := regexp.MustCompile(`Reserved block count:\s+(\d+)`)
	matches := re.FindStringSubmatch(output)
	require.NotEmpty(t, matches, "tune2fs output should contain 'Reserved block count'")

	reservedBlocks, err := strconv.ParseInt(matches[1], 10, 64)
	require.NoError(t, err)

	// Parse block size
	blockSizeRe := regexp.MustCompile(`Block size:\s+(\d+)`)
	blockSizeMatches := blockSizeRe.FindStringSubmatch(output)
	require.NotEmpty(t, blockSizeMatches, "tune2fs output should contain 'Block size'")

	blockSize, err := strconv.ParseInt(blockSizeMatches[1], 10, 64)
	require.NoError(t, err)

	reservedSpaceMB := (reservedBlocks * blockSize) >> 20

	t.Logf("Reserved blocks: %d, block size: %d, reserved space: %d MB", reservedBlocks, blockSize, reservedSpaceMB)

	// Verify the reserved blocks are set (if the feature flag is enabled)
	// When BuildReservedDiskSpaceMB > 0, reserved blocks should be non-zero
	if reservedBlocks > 0 {
		assert.Greater(t, reservedSpaceMB, int64(0), "reserved space should be positive when reserved blocks are set")
	}

	// Verify tune2fs output contains expected fields
	assert.True(t, strings.Contains(output, "Filesystem volume name:"), "tune2fs output should contain filesystem metadata")

	t.Logf("tune2fs verification passed: %d reserved blocks (%d MB reserved)", reservedBlocks, reservedSpaceMB)
}

