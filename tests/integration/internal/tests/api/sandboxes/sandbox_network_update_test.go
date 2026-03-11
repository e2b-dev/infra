package sandboxes

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	reverseproxy "github.com/e2b-dev/infra/packages/shared/pkg/proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/pool"
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

// TestUpdateEgressConfig exercises all update scenarios using a single sandbox.
// Subtests run sequentially — each PUT fully replaces the previous config.
func TestUpdateEgressConfig(t *testing.T) { //nolint:tparallel // subtests are sequential
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
			name:    "deny_all_blocks_everything",
			denyOut: ptrS(blockAll),
			checks: []connectivityCheck{
				{"https://8.8.8.8", false},
				{"https://1.1.1.1", false},
			},
		},
		// ── allow + deny-all (allow takes precedence over deny) ──────
		{
			name:     "allow_single_ip_through_deny_all",
			allowOut: ptrS("8.8.8.8"),
			denyOut:  ptrS(blockAll),
			checks: []connectivityCheck{
				{"https://8.8.8.8", true},
				{"https://1.1.1.1", false},
			},
		},
		{
			name:     "replace_allowed_ip",
			allowOut: ptrS("1.1.1.1"),
			denyOut:  ptrS(blockAll),
			checks: []connectivityCheck{
				{"https://1.1.1.1", true},
				{"https://8.8.8.8", false},
			},
		},
		{
			name:     "allow_multiple_ips",
			allowOut: ptrS("8.8.8.8", "1.1.1.1"),
			denyOut:  ptrS(blockAll),
			checks: []connectivityCheck{
				{"https://8.8.8.8", true},
				{"https://1.1.1.1", true},
			},
		},
		{
			name:     "allow_cidr_range",
			allowOut: ptrS("8.8.8.0/24"),
			denyOut:  ptrS(blockAll),
			checks: []connectivityCheck{
				{"https://8.8.8.8", true},  // 8.8.8.8 is in 8.8.8.0/24
				{"https://1.1.1.1", false}, // 1.1.1.1 is not
			},
		},
		// ── domain-based rules (TCP proxy SNI matching) ──────────────
		{
			name:     "allow_domain",
			allowOut: ptrS("google.com"),
			denyOut:  ptrS(blockAll),
			checks: []connectivityCheck{
				{"https://google.com", true},
				{"https://cloudflare.com", false},
			},
		},
		{
			name:     "allow_domain_and_ip",
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
			name:    "remove_allow_keep_deny",
			denyOut: ptrS(blockAll),
			checks: []connectivityCheck{
				{"https://google.com", false}, // previously allowed domain now blocked
				{"https://8.8.8.8", false},
			},
		},
		// ── clear all rules: back to default-allow ───────────────────
		{
			name: "clear_all_restores_access",
			checks: []connectivityCheck{
				{"https://8.8.8.8", true},
				{"https://1.1.1.1", true},
			},
		},
		// ── re-apply after clear: sets can be repopulated ────────────
		{
			name:     "reapply_after_clear",
			allowOut: ptrS("1.1.1.1"),
			denyOut:  ptrS(blockAll),
			checks: []connectivityCheck{
				{"https://1.1.1.1", true},
				{"https://8.8.8.8", false},
			},
		},
		// ── allow IP without deny: no blocking, allow set is no-op ───
		{
			name:     "allow_ip_without_deny_no_blocking",
			allowOut: ptrS("8.8.8.8"),
			checks: []connectivityCheck{
				{"https://8.8.8.8", true},
				{"https://1.1.1.1", true}, // no deny → default accept
			},
		},
		// ── final clear ──────────────────────────────────────────────
		{
			name: "final_clear",
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

// ingressRules is a builder for ingress config updates.
type ingressRules struct {
	body api.PutSandboxesSandboxIDNetworkJSONRequestBody
}

func ingress() *ingressRules { return &ingressRules{} }
func (r *ingressRules) denyPorts(ports ...int) *ingressRules {
	r.body.DenyPorts = &ports

	return r
}

func (r *ingressRules) allowPorts(ports ...int) *ingressRules {
	r.body.AllowPorts = &ports

	return r
}

func (r *ingressRules) denyIn(cidrs ...string) *ingressRules {
	r.body.DenyIn = &cidrs

	return r
}

func (r *ingressRules) allowIn(cidrs ...string) *ingressRules {
	r.body.AllowIn = &cidrs

	return r
}

func (r *ingressRules) maskHost(h string) *ingressRules {
	r.body.MaskRequestHost = &h

	return r
}

func TestUpdateIngressConfig(t *testing.T) { //nolint:tparallel // subtests are sequential
	t.Parallel()

	ctx := t.Context()
	client := setup.GetAPIClient()
	envdClient := setup.GetEnvdClient(t, ctx)

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTimeout(120),
		utils.WithAutoPause(false),
	)

	testPort := 8000
	proxyURL, err := url.Parse(setup.EnvdProxy)
	require.NoError(t, err)
	httpClient := &http.Client{Timeout: 10 * time.Second}

	apply := func(r *ingressRules) {
		t.Helper()
		resp := putNetwork(t, ctx, client, sbx.SandboxID, r.body)
		require.Equal(t, http.StatusNoContent, resp.StatusCode())
	}

	proxyStatus := func(port int, fromIP string) int {
		t.Helper()
		var headers *http.Header
		if fromIP != "" {
			headers = &http.Header{reverseproxy.ClientIPHeader: []string{fromIP}}
		}
		req := utils.NewRequest(sbx, proxyURL, port, headers)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()

		return resp.StatusCode
	}

	// Start HTTP servers so the proxy connects immediately instead of retrying.
	for _, p := range []int{testPort, testPort + 1} {
		err = utils.ExecCommand(t, ctx, sbx, envdClient, "sh", "-c",
			fmt.Sprintf("nohup python3 -m http.server %d >/dev/null 2>&1 &", p))
		require.NoError(t, err)
	}

	// Each check: port to hit, optional spoofed IP, whether we expect 403.
	type check struct {
		port    int
		fromIP  string
		blocked bool
	}

	type step struct {
		name   string
		rules  *ingressRules
		checks []check
		ciOnly bool // skip when running against GCP (client-proxy overwrites spoofed IP)
	}

	envdPort := int(consts.DefaultEnvdServerPort)
	isCI := strings.Contains(setup.EnvdProxy, "localhost") || strings.Contains(setup.EnvdProxy, "127.0.0.1")

	steps := []step{
		// ── Port deny/allow ──────────────────────────────────────────
		{
			name:  "port_deny_blocks_access",
			rules: ingress().denyPorts(testPort),
			checks: []check{
				{testPort, "", true},
				{testPort + 1, "", false},
			},
		},
		{
			name:  "port_allow_overrides_deny",
			rules: ingress().allowPorts(testPort).denyPorts(testPort),
			checks: []check{
				{testPort, "", false},
			},
		},
		// ── Client IP deny/allow ─────────────────────────────────────
		{
			// Both IPv4 and IPv6 "match all" CIDRs — CI may use ::1 (IPv6 loopback).
			name:  "client_ip_deny_all_blocks",
			rules: ingress().denyIn("0.0.0.0/0", "::/0"),
			checks: []check{
				{testPort, "", true},
			},
		},
		{
			name:  "client_ip_allow_all_overrides_deny_all",
			rules: ingress().allowIn("0.0.0.0/0", "::/0").denyIn("0.0.0.0/0", "::/0"),
			checks: []check{
				{testPort, "", false},
			},
		},
		{
			// Deny a reserved TEST-NET range (198.51.100.0/24, RFC 5737) that no real
			// machine uses. Our real IP won't match → request goes through.
			name:  "client_ip_deny_narrow_cidr_does_not_block_us",
			rules: ingress().denyIn("198.51.100.0/24"),
			checks: []check{
				{testPort, "", false},
			},
		},
		{
			// Deny both halves of IPv4 + IPv6 space to cover every possible real IP.
			name:  "client_ip_deny_both_halves_blocks",
			rules: ingress().denyIn("0.0.0.0/1", "128.0.0.0/1", "::/1", "8000::/1"),
			checks: []check{
				{testPort, "", true},
			},
		},
		{
			name:  "client_ip_allow_overrides_deny_all",
			rules: ingress().allowIn("0.0.0.0/1", "128.0.0.0/1", "::/1", "8000::/1").denyIn("0.0.0.0/0", "::/0"),
			checks: []check{
				{testPort, "", false},
			},
		},
		// ── Spoofed X-E2B-Client-IP (CI-only, bypass client-proxy) ──
		{
			name:   "spoofed_ip_deny_specific_cidr_blocks",
			ciOnly: true,
			rules:  ingress().denyIn("203.0.113.0/24"),
			checks: []check{
				{testPort, "203.0.113.42", true},
				{testPort, "198.51.100.1", false},
			},
		},
		{
			name:   "spoofed_ip_allow_overrides_deny",
			ciOnly: true,
			rules:  ingress().allowIn("203.0.113.42/32").denyIn("0.0.0.0/0", "203.0.113.0/24"),
			checks: []check{
				{testPort, "203.0.113.42", false},
				{testPort, "203.0.113.99", true},
			},
		},
		// ── Envd port exempt from both port deny and client IP deny ──
		{
			name:  "envd_exempt_from_ingress_restrictions",
			rules: ingress().denyPorts(envdPort).denyIn("0.0.0.0/0"),
			checks: []check{
				{envdPort, "", false},
			},
		},
		// ── Empty PUT clears ingress rules ───────────────────────────
		{
			name:  "clear_restores_access",
			rules: ingress(),
			checks: []check{
				{testPort, "", false},
			},
		},
	}

	for _, s := range steps { //nolint:paralleltest // sequential
		if s.ciOnly && !isCI {
			continue
		}
		t.Run(s.name, func(t *testing.T) {
			apply(s.rules)
			for _, c := range s.checks {
				got := proxyStatus(c.port, c.fromIP)
				if c.blocked {
					require.Equal(t, http.StatusForbidden, got, "port=%d fromIP=%q should be blocked", c.port, c.fromIP)
				} else {
					require.NotEqual(t, http.StatusForbidden, got, "port=%d fromIP=%q should not be blocked", c.port, c.fromIP)
				}
			}
		})
	}

	// ── Pause/resume preserves ingress rules (must be last) ─────────────

	t.Run("pause_resume_preserves_ingress_rules", func(t *testing.T) {
		apply(ingress().denyPorts(testPort))
		require.Equal(t, http.StatusForbidden, proxyStatus(testPort, ""))

		pauseResp, err := client.PostSandboxesSandboxIDPauseWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, pauseResp.StatusCode())

		resumeResp, err := client.PostSandboxesSandboxIDResumeWithResponse(ctx, sbx.SandboxID,
			api.PostSandboxesSandboxIDResumeJSONRequestBody{},
			setup.WithAPIKey(),
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resumeResp.StatusCode())

		require.Equal(t, http.StatusForbidden, proxyStatus(testPort, ""))
	})
}

// =============================================================================
// TestUpdateCombinedEgressAndIngress verifies that egress and ingress rules
// can be set in a single PUT and both take effect simultaneously.
// =============================================================================

func TestUpdateCombinedEgressAndIngress(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	client := setup.GetAPIClient()
	envdClient := setup.GetEnvdClient(t, ctx)

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTimeout(120),
		utils.WithAutoPause(false),
	)

	testPort := 8000
	proxyURL, err := url.Parse(setup.EnvdProxy)
	require.NoError(t, err)
	httpClient := &http.Client{Timeout: 10 * time.Second}

	// Start an HTTP server so the proxy connects.
	err = utils.ExecCommand(t, ctx, sbx, envdClient, "sh", "-c",
		fmt.Sprintf("nohup python3 -m http.server %d >/dev/null 2>&1 &", testPort))
	require.NoError(t, err)

	// Wait for the server to be reachable.
	waitResp := utils.WaitForStatus(t, httpClient, sbx, proxyURL, testPort, nil, http.StatusOK)
	waitResp.Body.Close()

	// Single PUT: deny all egress + deny port for ingress.
	resp := putNetwork(t, ctx, client, sbx.SandboxID, api.PutSandboxesSandboxIDNetworkJSONRequestBody{
		DenyOut:   ptrS(blockAll),
		DenyPorts: &[]int{testPort},
	})
	require.Equal(t, http.StatusNoContent, resp.StatusCode())

	// Egress: outbound blocked.
	verifyConnectivity(t, ctx, sbx, envdClient, []connectivityCheck{
		{"https://8.8.8.8", false},
	})

	// Ingress: port blocked.
	req := utils.NewRequest(sbx, proxyURL, testPort, nil)
	proxyResp, err := httpClient.Do(req)
	require.NoError(t, err)
	proxyResp.Body.Close()
	require.Equal(t, http.StatusForbidden, proxyResp.StatusCode)

	// Clear both: empty body restores defaults.
	resp = putNetwork(t, ctx, client, sbx.SandboxID, api.PutSandboxesSandboxIDNetworkJSONRequestBody{})
	require.Equal(t, http.StatusNoContent, resp.StatusCode())

	// Egress: outbound restored.
	verifyConnectivity(t, ctx, sbx, envdClient, []connectivityCheck{
		{"https://8.8.8.8", true},
	})

	// Ingress: port restored.
	req = utils.NewRequest(sbx, proxyURL, testPort, nil)
	proxyResp, err = httpClient.Do(req)
	require.NoError(t, err)
	proxyResp.Body.Close()
	require.NotEqual(t, http.StatusForbidden, proxyResp.StatusCode)
}

// =============================================================================
// TestUpdateMaskRequestHost exercises dynamic MaskRequestHost updates.
// A Python server echoes the Host header back so we can verify masking.
// =============================================================================

func TestUpdateMaskRequestHost(t *testing.T) { //nolint:tparallel // subtests are sequential
	t.Parallel()

	ctx := t.Context()
	client := setup.GetAPIClient()
	envdClient := setup.GetEnvdClient(t, ctx)

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTimeout(120),
		utils.WithAutoPause(false),
	)

	testPort := 8000
	proxyURL, err := url.Parse(setup.EnvdProxy)
	require.NoError(t, err)
	httpClient := &http.Client{Timeout: 10 * time.Second}

	// Start a Python server that echoes the Host header in the response body.
	echoServer := fmt.Sprintf(`
import http.server, socketserver
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(self.headers.get("Host","").encode())
    def log_message(self, *a): pass
socketserver.TCPServer(("", %d), H).serve_forever()
`, testPort)
	err = utils.ExecCommand(t, ctx, sbx, envdClient, "sh", "-c",
		"nohup python3 -c '"+echoServer+"' >/dev/null 2>&1 &")
	require.NoError(t, err)

	// Wait for the echo server to be ready.
	resp := utils.WaitForStatus(t, httpClient, sbx, proxyURL, testPort, nil, http.StatusOK)
	resp.Body.Close()

	// Returns the Host header as seen by the server inside the sandbox.
	getHost := func() string {
		t.Helper()
		req := utils.NewRequest(sbx, proxyURL, testPort, nil)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		return string(body)
	}

	apply := func(r *ingressRules) {
		t.Helper()
		resp := putNetwork(t, ctx, client, sbx.SandboxID, r.body)
		require.Equal(t, http.StatusNoContent, resp.StatusCode())
	}

	// Verify baseline — Host should contain the sandbox routing domain, not a mask.
	t.Run("baseline_no_mask", func(t *testing.T) { //nolint:paralleltest // sequential
		host := getHost()
		require.NotEmpty(t, host)
		require.NotContains(t, host, "masked-host")
	})

	maskedTemplate := fmt.Sprintf("masked-host:%s", pool.MaskRequestHostPortPlaceholder)
	maskedExpected := fmt.Sprintf("masked-host:%d", testPort)

	t.Run("set_mask_with_port_placeholder", func(t *testing.T) { //nolint:paralleltest // sequential
		apply(ingress().maskHost(maskedTemplate))
		require.Equal(t, maskedExpected, getHost())
	})

	t.Run("update_mask", func(t *testing.T) { //nolint:paralleltest // sequential
		apply(ingress().maskHost("other-host:9999"))
		require.Equal(t, "other-host:9999", getHost())
	})

	t.Run("clear_mask", func(t *testing.T) { //nolint:paralleltest // sequential
		// Empty ingress() sets MaskRequestHost to nil — clears the mask.
		apply(ingress())
		host := getHost()
		require.NotEqual(t, "other-host:9999", host)
		require.NotContains(t, host, "masked-host")
	})

	t.Run("set_mask_again", func(t *testing.T) { //nolint:paralleltest // sequential
		apply(ingress().maskHost(maskedTemplate))
		require.Equal(t, maskedExpected, getHost())
	})
}
