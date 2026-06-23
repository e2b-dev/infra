package envd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

// TestInitFragmentationHarness measures how envd's resident working set affects
// resume latency: it inflates envd's heap, pauses, and times the resume.
// Requires envd built with `make build-fragment`; gated behind TESTS_ENVD_FRAGMENT
// so it never runs in normal CI. Override sizes with TESTS_ENVD_FRAGMENT_MB.
func TestInitFragmentationHarness(t *testing.T) {
	if os.Getenv("TESTS_ENVD_FRAGMENT") == "" {
		t.Skip("set TESTS_ENVD_FRAGMENT=1 (and deploy envd built with `make build-fragment`) to run")
	}

	sizes := parseSizes(os.Getenv("TESTS_ENVD_FRAGMENT_MB"))
	c := setup.GetAPIClient()

	type result struct {
		mb       int
		resumeMs int64
	}
	results := make([]result, 0, len(sizes))

	for _, mb := range sizes {
		d := measureResumeWithFragmentation(t, c, mb)
		results = append(results, result{mb: mb, resumeMs: d.Milliseconds()})
		t.Logf("fragment_mb=%d resume_ms=%d", mb, d.Milliseconds())
	}

	t.Log("=== init fragmentation harness ===")
	for _, r := range results {
		t.Logf("%6d MiB extra envd heap -> resume %5d ms", r.mb, r.resumeMs)
	}
}

func measureResumeWithFragmentation(t *testing.T, c *api.ClientWithResponses, mb int) time.Duration {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()

	sbx := utils.SetupSandboxWithCleanup(t, c, utils.WithTimeout(120), utils.WithAutoPause(false))

	if mb > 0 {
		fragmentEnvd(t, ctx, sbx.SandboxID, mb)
	}

	pauseResp, err := c.PostSandboxesSandboxIDPauseWithResponse(ctx, sbx.SandboxID, api.PostSandboxesSandboxIDPauseJSONRequestBody{}, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, pauseResp.StatusCode(), "pause failed: %s", string(pauseResp.Body))

	sbxIDWithClient := sbx.SandboxID + "-" + sbx.ClientID

	start := time.Now()
	resumeResp, err := c.PostSandboxesSandboxIDResumeWithResponse(ctx, sbxIDWithClient, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
	require.NoError(t, err)
	dur := time.Since(start)
	require.Equal(t, http.StatusCreated, resumeResp.StatusCode(), "resume failed: %s", string(resumeResp.Body))

	return dur
}

func fragmentEnvd(t *testing.T, ctx context.Context, sandboxID string, mb int) {
	t.Helper()

	url := fmt.Sprintf("%s/debug/fragment?mb=%d", setup.EnvdProxy, mb)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	require.NoError(t, err)
	require.NoError(t, setup.WithSandbox(t, sandboxID)(ctx, req))

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode,
		"/debug/fragment unavailable; deploy envd built with `make build-fragment`")
}

func parseSizes(env string) []int {
	if env == "" {
		return []int{0, 256, 512, 1024}
	}
	var sizes []int
	for _, part := range strings.Split(env, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err == nil {
			sizes = append(sizes, n)
		}
	}
	if len(sizes) == 0 {
		return []int{0}
	}
	return sizes
}
