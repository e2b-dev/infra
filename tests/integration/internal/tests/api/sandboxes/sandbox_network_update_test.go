package sandboxes

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	proxygrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/proxy"
	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

// =============================================================================
// PUT /sandboxes/{sandboxID}/network — Dynamic network config update tests
// =============================================================================

const blockAll = sandbox_network.AllInternetTrafficCIDR

func ptrS(s ...string) *[]string { return &s }
func ptrI(i ...int) *[]int       { return &i }

// putNetwork is a helper to call the update network endpoint.
func putNetwork(
	t *testing.T,
	ctx context.Context,
	client *api.ClientWithResponses,
	sandboxID string,
	body api.PutSandboxesSandboxIDNetworkJSONRequestBody,
) *api.PutSandboxesSandboxIDNetworkResponse {
	t.Helper()
	resp, err := client.PutSandboxesSandboxIDNetworkWithResponse(
		ctx, sandboxID, body, setup.WithAPIKey(),
	)
	require.NoError(t, err)

	return resp
}

// connectivityCheck describes a URL that should be reachable or blocked.
type connectivityCheck struct {
	url     string
	allowed bool
}

func verifyConnectivity(
	t *testing.T,
	ctx context.Context,
	sbx *api.Sandbox,
	envdClient *setup.EnvdClient,
	checks []connectivityCheck,
) {
	t.Helper()
	for _, c := range checks {
		if c.allowed {
			assertSuccessfulHTTPRequest(t, ctx, sbx, envdClient, c.url, c.url+" should be reachable")
		} else {
			assertBlockedHTTPRequest(t, ctx, sbx, envdClient, c.url, c.url+" should be blocked")
		}
	}
}

// TestUpdateNetworkConfig exercises all update scenarios using a single sandbox.
// Subtests run sequentially — each PUT fully replaces the previous config.
func TestUpdateNetworkConfig(t *testing.T) { //nolint:tparallel // subtests are sequential
	t.Parallel()

	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()
	envdClient := setup.GetEnvdClient(t, ctx)

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(120),
		utils.WithAutoPause(false),
	)

	// ── Error responses (no sandbox needed) ──────────────────────────────

	t.Run("not_found", func(t *testing.T) { //nolint:paralleltest // sequential
		resp := putNetwork(t, ctx, client, "ixxxxxxxxxxxxxxxxxx0",
			api.PutSandboxesSandboxIDNetworkJSONRequestBody{AllowOut: ptrS("8.8.8.8")},
		)
		require.Equal(t, http.StatusNotFound, resp.StatusCode())
	})

	t.Run("unauthorized", func(t *testing.T) { //nolint:paralleltest // sequential
		resp, err := client.PutSandboxesSandboxIDNetworkWithResponse(
			ctx, "any-sandbox-id", api.PutSandboxesSandboxIDNetworkJSONRequestBody{},
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode())
	})

	// ── Input validation: rejected (400) ─────────────────────────────────

	rejectedCases := []struct {
		name     string
		allowOut *[]string
		denyOut  *[]string
	}{
		// denyOut must be IPs/CIDRs only
		{"domain_in_deny_out", nil, ptrS("example.com")},
		{"garbage_in_deny_out", nil, ptrS("not-a-cidr")},
		{"domain_in_deny_out_alongside_block_all", nil, ptrS(blockAll, "example.com")},
		// domains in allowOut require deny-all in denyOut
		{"domain_allow_without_deny", ptrS("google.com"), nil},
		{"domain_allow_with_partial_deny", ptrS("google.com"), ptrS("10.0.0.0/8")},
		{"wildcard_domain_without_deny_all", ptrS("*.example.com"), nil},
		{"wildcard_domain_with_partial_deny", ptrS("*.example.com"), ptrS("10.0.0.0/8")},
		{"mixed_domain_ip_without_deny_all", ptrS("example.com", "8.8.8.8"), ptrS("10.0.0.0/8")},
	}
	for _, tc := range rejectedCases {
		t.Run("reject/"+tc.name, func(t *testing.T) {
			resp := putNetwork(t, ctx, client, sbx.SandboxID, api.PutSandboxesSandboxIDNetworkJSONRequestBody{
				AllowOut: tc.allowOut,
				DenyOut:  tc.denyOut,
			})
			require.Equal(t, http.StatusBadRequest, resp.StatusCode())
		})
	}

	// ── Input validation: accepted (204, no connectivity check) ──────────

	acceptedCases := []struct {
		name     string
		allowOut *[]string
		denyOut  *[]string
	}{
		{"empty_body", nil, nil},
		{"ip_allow_without_deny", ptrS("8.8.8.8"), nil},
		{"ip_allow_with_partial_deny", ptrS("8.8.8.8"), ptrS("10.0.0.0/8")},
		{"cidr_allow_without_deny", ptrS("8.8.0.0/16"), nil},
		{"deny_all_only", nil, ptrS(blockAll)},
		{"ip_allow_with_deny_all", ptrS("8.8.8.8"), ptrS(blockAll)},
		{"domain_with_deny_all", ptrS("google.com"), ptrS(blockAll)},
		{"wildcard_domain_with_deny_all", ptrS("*.example.com"), ptrS(blockAll)},
		{"mixed_domain_ip_with_deny_all", ptrS("example.com", "8.8.8.8"), ptrS(blockAll)},
		{"multiple_cidrs_in_deny", nil, ptrS("10.0.0.0/8", "192.168.0.0/16")},
	}
	for _, tc := range acceptedCases {
		t.Run("accept/"+tc.name, func(t *testing.T) {
			resp := putNetwork(t, ctx, client, sbx.SandboxID, api.PutSandboxesSandboxIDNetworkJSONRequestBody{
				AllowOut: tc.allowOut,
				DenyOut:  tc.denyOut,
			})
			require.Equal(t, http.StatusNoContent, resp.StatusCode())
		})
	}

	// Reset to clean state before firewall steps.
	t.Run("reset_before_firewall_steps", func(t *testing.T) { //nolint:paralleltest // sequential
		resp := putNetwork(t, ctx, client, sbx.SandboxID, api.PutSandboxesSandboxIDNetworkJSONRequestBody{})
		require.Equal(t, http.StatusNoContent, resp.StatusCode())
		verifyConnectivity(t, ctx, sbx, envdClient, []connectivityCheck{
			{"https://8.8.8.8", true},
			{"https://1.1.1.1", true},
		})
	})

	// ── Firewall rule updates (table-driven, apply + verify connectivity) ─

	type step struct {
		name     string
		allowOut *[]string
		denyOut  *[]string
		checks   []connectivityCheck
	}

	// Steps execute sequentially. Each PUT fully replaces the previous config.
	// The order tests that rule sets (allow, deny) interact correctly:
	//   nftables rule evaluation order:
	//     1. ESTABLISHED/RELATED → accept
	//     2. predefinedAllowSet → accept
	//     3. predefinedDenySet → drop
	//     4. userAllowSet (non-TCP) → accept  | TCP → proxy (allow/deny by SNI/IP)
	//     5. userDenySet  (non-TCP) → drop    |
	//     6. default: accept
	steps := []step{
		// ── deny-only rules ──────────────────────────────────────────
		{
			name:    "1_deny_all_blocks_everything",
			denyOut: ptrS(blockAll),
			checks: []connectivityCheck{
				{"https://8.8.8.8", false},
				{"https://1.1.1.1", false},
			},
		},
		// ── allow + deny-all (allow takes precedence over deny) ──────
		{
			name:     "2_allow_single_ip_through_deny_all",
			allowOut: ptrS("8.8.8.8"),
			denyOut:  ptrS(blockAll),
			checks: []connectivityCheck{
				{"https://8.8.8.8", true},
				{"https://1.1.1.1", false},
			},
		},
		{
			name:     "3_replace_allowed_ip",
			allowOut: ptrS("1.1.1.1"),
			denyOut:  ptrS(blockAll),
			checks: []connectivityCheck{
				{"https://1.1.1.1", true},
				{"https://8.8.8.8", false},
			},
		},
		{
			name:     "4_allow_multiple_ips",
			allowOut: ptrS("8.8.8.8", "1.1.1.1"),
			denyOut:  ptrS(blockAll),
			checks: []connectivityCheck{
				{"https://8.8.8.8", true},
				{"https://1.1.1.1", true},
			},
		},
		{
			name:     "5_allow_cidr_range",
			allowOut: ptrS("8.8.8.0/24"),
			denyOut:  ptrS(blockAll),
			checks: []connectivityCheck{
				{"https://8.8.8.8", true},  // 8.8.8.8 is in 8.8.8.0/24
				{"https://1.1.1.1", false}, // 1.1.1.1 is not
			},
		},
		// ── domain-based rules (TCP proxy SNI matching) ──────────────
		{
			name:     "6_allow_domain",
			allowOut: ptrS("google.com"),
			denyOut:  ptrS(blockAll),
			checks: []connectivityCheck{
				{"https://google.com", true},
				{"https://cloudflare.com", false},
			},
		},
		{
			name:     "7_allow_domain_and_ip",
			allowOut: ptrS("google.com", "1.1.1.1"),
			denyOut:  ptrS(blockAll),
			checks: []connectivityCheck{
				{"https://google.com", true},
				{"https://1.1.1.1", true},
				{"https://cloudflare.com", false},
			},
		},
		// ── replacement semantics: PUT replaces, not appends ─────────
		{
			name:    "8_remove_allow_keep_deny",
			denyOut: ptrS(blockAll),
			checks: []connectivityCheck{
				{"https://google.com", false}, // previously allowed domain now blocked
				{"https://8.8.8.8", false},
			},
		},
		// ── clear all rules: back to default-allow ───────────────────
		{
			name: "9_clear_all_rules_restores_access",
			checks: []connectivityCheck{
				{"https://8.8.8.8", true},
				{"https://1.1.1.1", true},
			},
		},
		// ── re-apply after clear: sets can be repopulated ────────────
		{
			name:     "10_reapply_rules_after_clear",
			allowOut: ptrS("1.1.1.1"),
			denyOut:  ptrS(blockAll),
			checks: []connectivityCheck{
				{"https://1.1.1.1", true},
				{"https://8.8.8.8", false},
			},
		},
		// ── allow IP without deny: no blocking, allow set is no-op ───
		{
			name:     "11_allow_ip_without_deny_no_blocking",
			allowOut: ptrS("8.8.8.8"),
			checks: []connectivityCheck{
				{"https://8.8.8.8", true},
				{"https://1.1.1.1", true}, // no deny → default accept
			},
		},
		// ── final clear ──────────────────────────────────────────────
		{
			name: "12_final_clear",
			checks: []connectivityCheck{
				{"https://8.8.8.8", true},
				{"https://1.1.1.1", true},
			},
		},
	}

	for _, s := range steps { //nolint:paralleltest // subtests are sequential
		t.Run(s.name, func(t *testing.T) {
			resp := putNetwork(t, ctx, client, sbx.SandboxID, api.PutSandboxesSandboxIDNetworkJSONRequestBody{
				AllowOut: s.allowOut,
				DenyOut:  s.denyOut,
			})
			require.Equal(t, http.StatusNoContent, resp.StatusCode())
			verifyConnectivity(t, ctx, sbx, envdClient, s.checks)
		})
	}

	// ── Pause/resume (must be last — changes sandbox lifecycle) ───────────

	t.Run("pause_resume_preserves_rules", func(t *testing.T) { //nolint:paralleltest // sequential
		// Apply rules
		resp := putNetwork(t, ctx, client, sbx.SandboxID, api.PutSandboxesSandboxIDNetworkJSONRequestBody{
			AllowOut: ptrS("8.8.8.8"),
			DenyOut:  ptrS(blockAll),
		})
		require.Equal(t, http.StatusNoContent, resp.StatusCode())
		verifyConnectivity(t, ctx, sbx, envdClient, []connectivityCheck{
			{"https://8.8.8.8", true},
			{"https://1.1.1.1", false},
		})

		// Pause
		pauseResp, err := client.PostSandboxesSandboxIDPauseWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, pauseResp.StatusCode())

		// Resume
		resumeResp, err := client.PostSandboxesSandboxIDResumeWithResponse(ctx, sbx.SandboxID,
			api.PostSandboxesSandboxIDResumeJSONRequestBody{},
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resumeResp.StatusCode())

		// Verify rules survived
		verifyConnectivity(t, ctx, sbx, envdClient, []connectivityCheck{
			{"https://8.8.8.8", true},
			{"https://1.1.1.1", false},
		})
	})
}

// =============================================================================
// TestUpdateIngressConfig exercises ingress control (port + client IP filtering)
// via the PUT /sandboxes/{sandboxID}/network endpoint.
// Subtests run sequentially — each PUT fully replaces the previous config.
// =============================================================================

// proxyRequest makes an HTTP request to the sandbox proxy on the given port
// and returns the status code. The response body is drained and closed.
func proxyRequest(
	t *testing.T,
	sbx *api.Sandbox,
	port int,
	extraHeaders *http.Header,
) int {
	t.Helper()

	proxyURL, err := url.Parse(setup.EnvdProxy)
	require.NoError(t, err)

	client := &http.Client{Timeout: 10 * time.Second}
	req := utils.NewRequest(sbx, proxyURL, port, extraHeaders)
	resp, err := client.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	return resp.StatusCode
}

func TestUpdateIngressConfig(t *testing.T) { //nolint:tparallel // subtests are sequential
	t.Parallel()

	ctx := t.Context()
	client := setup.GetAPIClient()

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTimeout(120),
		utils.WithAutoPause(false),
		utils.WithSecure(true),
	)

	// No server needed — we only need to distinguish 403 (ingress denied)
	// from 502 (port not open, but ingress allowed).
	testPort := 8000
	proxyURL, err := url.Parse(setup.EnvdProxy)
	require.NoError(t, err)
	httpClient := &http.Client{Timeout: 10 * time.Second}

	// ── Port deny/allow through real proxy ──────────────────────────────

	t.Run("port_deny_blocks_access", func(t *testing.T) { //nolint:paralleltest // sequential
		resp := putNetwork(t, ctx, client, sbx.SandboxID, api.PutSandboxesSandboxIDNetworkJSONRequestBody{
			DenyPorts: ptrI(testPort),
		})
		require.Equal(t, http.StatusNoContent, resp.StatusCode())

		// Denied port → 403.
		require.Equal(t, http.StatusForbidden, proxyRequest(t, sbx, testPort, nil))
		// Other port is still reachable (not 403).
		require.NotEqual(t, http.StatusForbidden, proxyRequest(t, sbx, testPort+1, nil))
	})

	t.Run("port_allow_overrides_deny", func(t *testing.T) { //nolint:paralleltest // sequential
		resp := putNetwork(t, ctx, client, sbx.SandboxID, api.PutSandboxesSandboxIDNetworkJSONRequestBody{
			AllowPorts: ptrI(testPort),
			DenyPorts:  ptrI(testPort),
		})
		require.Equal(t, http.StatusNoContent, resp.StatusCode())

		// Allow wins over deny — not 403.
		require.NotEqual(t, http.StatusForbidden, proxyRequest(t, sbx, testPort, nil))
	})

	// ── Client IP deny/allow through real proxy ─────────────────────────

	t.Run("client_ip_deny_blocks_access", func(t *testing.T) { //nolint:paralleltest // sequential
		resp := putNetwork(t, ctx, client, sbx.SandboxID, api.PutSandboxesSandboxIDNetworkJSONRequestBody{
			DenyIn: ptrS("0.0.0.0/0"),
		})
		require.Equal(t, http.StatusNoContent, resp.StatusCode())

		require.Equal(t, http.StatusForbidden, proxyRequest(t, sbx, testPort, nil))
	})

	t.Run("client_ip_allow_overrides_deny", func(t *testing.T) { //nolint:paralleltest // sequential
		resp := putNetwork(t, ctx, client, sbx.SandboxID, api.PutSandboxesSandboxIDNetworkJSONRequestBody{
			AllowIn: ptrS("0.0.0.0/0"),
			DenyIn:  ptrS("0.0.0.0/0"),
		})
		require.Equal(t, http.StatusNoContent, resp.StatusCode())

		// Allow wins over deny — not 403.
		require.NotEqual(t, http.StatusForbidden, proxyRequest(t, sbx, testPort, nil))
	})

	// ── Envd port exempt from both port deny and client IP deny ─────────

	t.Run("envd_exempt_from_ingress_restrictions", func(t *testing.T) { //nolint:paralleltest // sequential
		// Both port deny and client IP deny active — envd should still work.
		resp := putNetwork(t, ctx, client, sbx.SandboxID, api.PutSandboxesSandboxIDNetworkJSONRequestBody{
			DenyPorts: ptrI(int(consts.DefaultEnvdServerPort)),
			DenyIn:    ptrS("0.0.0.0/0"),
		})
		require.Equal(t, http.StatusNoContent, resp.StatusCode())

		envdURL := *proxyURL
		envdURL.Path = "/health"
		headers := &http.Header{proxygrpc.MetadataEnvdHTTPAccessToken: []string{*sbx.EnvdAccessToken}}
		req := utils.NewRequest(sbx, &envdURL, int(consts.DefaultEnvdServerPort), headers)
		r, err := httpClient.Do(req)
		require.NoError(t, err)
		r.Body.Close()
		require.Equal(t, http.StatusNoContent, r.StatusCode)
	})

	// ── Replacement: empty PUT clears ingress rules ─────────────────────

	t.Run("clear_restores_access", func(t *testing.T) { //nolint:paralleltest // sequential
		resp := putNetwork(t, ctx, client, sbx.SandboxID, api.PutSandboxesSandboxIDNetworkJSONRequestBody{})
		require.Equal(t, http.StatusNoContent, resp.StatusCode())

		// No longer 403 — port is accessible again (502 = nothing listening, but ingress allowed).
		require.NotEqual(t, http.StatusForbidden, proxyRequest(t, sbx, testPort, nil))
	})

	// ── Pause/resume preserves ingress rules (must be last) ─────────────

	t.Run("pause_resume_preserves_ingress_rules", func(t *testing.T) { //nolint:paralleltest // sequential
		resp := putNetwork(t, ctx, client, sbx.SandboxID, api.PutSandboxesSandboxIDNetworkJSONRequestBody{
			DenyPorts: ptrI(testPort),
		})
		require.Equal(t, http.StatusNoContent, resp.StatusCode())

		require.Equal(t, http.StatusForbidden, proxyRequest(t, sbx, testPort, nil))

		// Pause
		pauseResp, err := client.PostSandboxesSandboxIDPauseWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, pauseResp.StatusCode())

		// Resume
		resumeResp, err := client.PostSandboxesSandboxIDResumeWithResponse(ctx, sbx.SandboxID,
			api.PostSandboxesSandboxIDResumeJSONRequestBody{},
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resumeResp.StatusCode())

		// Port deny should survive pause/resume.
		require.Equal(t, http.StatusForbidden, proxyRequest(t, sbx, testPort, nil))
	})
}
