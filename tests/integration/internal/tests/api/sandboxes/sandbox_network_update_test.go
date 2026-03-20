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

	// ── Helpers ──────────────────────────────────────────────────────────

	updateIngress := func(allowIn, denyIn []string) {
		t.Helper()
		var a, d *[]string
		if allowIn != nil {
			a = &allowIn
		}
		if denyIn != nil {
			d = &denyIn
		}
		resp := putNetwork(t, ctx, client, sbx.SandboxID, api.PutSandboxesSandboxIDNetworkJSONRequestBody{
			AllowIn: a,
			DenyIn:  d,
		})
		require.Equal(t, http.StatusNoContent, resp.StatusCode())
	}

	updateAll := func(body api.PutSandboxesSandboxIDNetworkJSONRequestBody) {
		t.Helper()
		resp := putNetwork(t, ctx, client, sbx.SandboxID, body)
		require.Equal(t, http.StatusNoContent, resp.StatusCode())
	}

	resetRules := func() {
		t.Helper()
		updateAll(api.PutSandboxesSandboxIDNetworkJSONRequestBody{})
	}

	testPort := 8000
	echoPort := 8002
	proxyURL, err := url.Parse(setup.EnvdProxy)
	require.NoError(t, err)
	httpClient := &http.Client{Timeout: 10 * time.Second}

	// defaultClientIP is the IPv4 address used when no specific fromIP is
	// provided. Ingress rules only support IPv4; without this, CI runners
	// that connect via IPv6 would be unconditionally denied.
	const defaultClientIP = "203.0.113.99"

	request := func(port int, fromIP string) int {
		t.Helper()
		if fromIP == "" {
			fromIP = defaultClientIP
		}
		headers := &http.Header{"X-Forwarded-For": []string{fromIP + ", 0.0.0.0"}}
		req := utils.NewRequest(sbx, proxyURL, port, headers)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()

		return resp.StatusCode
	}

	// Start HTTP servers for ingress tests.
	for _, p := range []int{testPort, testPort + 1} {
		err = utils.ExecCommand(t, ctx, sbx, envdClient, "sh", "-c",
			fmt.Sprintf("nohup python3 -m http.server %d >/dev/null 2>&1 &", p))
		require.NoError(t, err)
	}

	// Start a Python echo server for mask tests (echoes Host header).
	echoServer := fmt.Sprintf(`
import http.server, socketserver
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.end_headers()
        self.wfile.write(self.headers.get("Host","").encode())
    def log_message(self, *a): pass
socketserver.TCPServer(("", %d), H).serve_forever()
`, echoPort)
	err = utils.ExecCommand(t, ctx, sbx, envdClient, "sh", "-c",
		"nohup python3 -c '"+echoServer+"' >/dev/null 2>&1 &")
	require.NoError(t, err)

	// Wait for servers to be reachable.
	waitResp := utils.WaitForStatus(t, httpClient, sbx, proxyURL, testPort, nil, http.StatusOK)
	waitResp.Body.Close()
	waitResp = utils.WaitForStatus(t, httpClient, sbx, proxyURL, echoPort, nil, http.StatusOK)
	waitResp.Body.Close()

	echoHost := func() string {
		t.Helper()
		req := utils.NewRequest(sbx, proxyURL, echoPort, nil)
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		return string(body)
	}

	envdPort := int(consts.DefaultEnvdServerPort)
	isCI := strings.Contains(setup.EnvdProxy, "localhost") || strings.Contains(setup.EnvdProxy, "127.0.0.1")

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
		// port syntax not supported for egress
		{"port_in_deny_out", nil, ptrS("10.0.0.0/8:22")},
		{"port_in_allow_out", ptrS("8.8.8.8:80"), nil},
		{"port_range_in_allow_out", ptrS("8.8.8.8:80-443"), nil},
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

	// ── Ingress validation: rejected (400) ───────────────────────────────

	rejectedIngressCases := []struct {
		name    string
		allowIn *[]string
		denyIn  *[]string
	}{
		{"domain_in_deny_in", nil, ptrS("example.com")},
		{"domain_in_allow_in", ptrS("example.com"), ptrS(blockAll)},
		{"allow_in_without_deny_all", ptrS("10.0.0.0/8"), nil},
		{"allow_in_with_partial_deny", ptrS("10.0.0.0/8"), ptrS("192.168.0.0/16")},
	}
	for _, tc := range rejectedIngressCases {
		t.Run("reject/ingress_"+tc.name, func(t *testing.T) {
			resp := putNetwork(t, ctx, client, sbx.SandboxID, api.PutSandboxesSandboxIDNetworkJSONRequestBody{
				AllowIn: tc.allowIn,
				DenyIn:  tc.denyIn,
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

	// ── Ingress validation: accepted (204) ───────────────────────────────

	acceptedIngressCases := []struct {
		name    string
		allowIn *[]string
		denyIn  *[]string
	}{
		{"deny_all", nil, ptrS(blockAll)},
		{"deny_cidr", nil, ptrS("10.0.0.0/8")},
		{"deny_ip", nil, ptrS("8.8.8.8")},
		{"deny_with_port", nil, ptrS("0.0.0.0/0:80")},
		{"deny_with_port_range", nil, ptrS("0.0.0.0/0:80-443")},
		{"allow_with_deny_all", ptrS("10.0.0.0/8"), ptrS(blockAll)},
		{"allow_ip_with_deny_all", ptrS("8.8.8.8"), ptrS(blockAll)},
	}
	for _, tc := range acceptedIngressCases {
		t.Run("accept/ingress_"+tc.name, func(t *testing.T) {
			resp := putNetwork(t, ctx, client, sbx.SandboxID, api.PutSandboxesSandboxIDNetworkJSONRequestBody{
				AllowIn: tc.allowIn,
				DenyIn:  tc.denyIn,
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

	// ── Egress: firewall rule updates (table-driven, apply + verify connectivity)

	type step struct {
		name     string
		allowOut *[]string
		denyOut  *[]string
		checks   []connectivityCheck
	}

	// Steps execute sequentially. Each PUT fully replaces the previous config.
	steps := []step{
		{
			name:    "1_deny_all_blocks_everything",
			denyOut: ptrS(blockAll),
			checks: []connectivityCheck{
				{"https://8.8.8.8", false},
				{"https://1.1.1.1", false},
			},
		},
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
				{"https://8.8.8.8", true},
				{"https://1.1.1.1", false},
			},
		},
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
		{
			name:    "8_remove_allow_keep_deny",
			denyOut: ptrS(blockAll),
			checks: []connectivityCheck{
				{"https://google.com", false},
				{"https://8.8.8.8", false},
			},
		},
		{
			name: "9_clear_all_rules_restores_access",
			checks: []connectivityCheck{
				{"https://8.8.8.8", true},
				{"https://1.1.1.1", true},
			},
		},
		{
			name:     "10_reapply_rules_after_clear",
			allowOut: ptrS("1.1.1.1"),
			denyOut:  ptrS(blockAll),
			checks: []connectivityCheck{
				{"https://1.1.1.1", true},
				{"https://8.8.8.8", false},
			},
		},
		{
			name:     "11_allow_ip_without_deny_no_blocking",
			allowOut: ptrS("8.8.8.8"),
			checks: []connectivityCheck{
				{"https://8.8.8.8", true},
				{"https://1.1.1.1", true},
			},
		},
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

	// =====================================================================
	// Ingress: port + client IP filtering
	// =====================================================================

	resetRules()

	type ingressCheck struct {
		port    int
		fromIP  string
		blocked bool
	}

	type ingressStep struct {
		name    string
		allowIn []string
		denyIn  []string
		checks  []ingressCheck
		ciOnly  bool
	}

	ingressSteps := []ingressStep{
		{
			name:   "port_deny_blocks_access",
			denyIn: []string{fmt.Sprintf("0.0.0.0/0:%d", testPort)},
			checks: []ingressCheck{{testPort, "", true}, {testPort + 1, "", false}},
		},
		{
			name:    "port_allow_overrides_deny",
			allowIn: []string{fmt.Sprintf("0.0.0.0/0:%d", testPort)}, denyIn: []string{sandbox_network.AllInternetTrafficCIDR},
			checks: []ingressCheck{{testPort, "", false}, {testPort + 1, "", true}},
		},
		{
			name:   "client_ip_deny_all_blocks",
			denyIn: []string{blockAll},
			checks: []ingressCheck{{testPort, "", true}},
		},
		{
			name:    "client_ip_allow_all_overrides_deny_all",
			allowIn: []string{blockAll}, denyIn: []string{blockAll},
			checks: []ingressCheck{{testPort, "", false}},
		},
		{
			name:   "client_ip_deny_narrow_cidr_does_not_block_us",
			denyIn: []string{"198.51.100.0/24"},
			checks: []ingressCheck{{testPort, "", false}},
		},
		{
			name:   "port_range_deny_blocks_range",
			denyIn: []string{fmt.Sprintf("0.0.0.0/0:%d-%d", testPort, testPort+1)},
			checks: []ingressCheck{{testPort, "", true}, {testPort + 1, "", true}},
		},
		{
			name:    "port_range_allow_overrides_deny",
			allowIn: []string{fmt.Sprintf("0.0.0.0/0:%d-%d", testPort, testPort+1)}, denyIn: []string{blockAll},
			checks: []ingressCheck{{testPort, "", false}, {testPort + 1, "", false}},
		},
		{
			name: "spoofed_ip_deny_specific_cidr_blocks", ciOnly: true,
			denyIn: []string{"203.0.113.0/24"},
			checks: []ingressCheck{{testPort, "203.0.113.42", true}, {testPort, "198.51.100.1", false}},
		},
		{
			name: "spoofed_ip_allow_overrides_deny", ciOnly: true,
			allowIn: []string{"203.0.113.42"}, denyIn: []string{blockAll, "203.0.113.0/24"},
			checks: []ingressCheck{{testPort, "203.0.113.42", false}, {testPort, "203.0.113.99", true}},
		},
		{
			name: "garbage_xff_fails_closed", ciOnly: true,
			denyIn: []string{"198.51.100.0/24"},
			checks: []ingressCheck{{testPort, "not-an-ip", true}},
		},
		{
			name:   "envd_exempt_from_ingress_restrictions",
			denyIn: []string{blockAll},
			checks: []ingressCheck{{envdPort, "", false}},
		},
		{
			name:   "clear_restores_access",
			checks: []ingressCheck{{testPort, "", false}},
		},
	}

	for _, s := range ingressSteps { //nolint:paralleltest // sequential
		if s.ciOnly && !isCI {
			continue
		}
		t.Run("ingress/"+s.name, func(t *testing.T) {
			updateIngress(s.allowIn, s.denyIn)
			for _, c := range s.checks {
				got := request(c.port, c.fromIP)
				if c.blocked {
					require.Equal(t, http.StatusForbidden, got, "port=%d fromIP=%q should be blocked", c.port, c.fromIP)
				} else {
					require.NotEqual(t, http.StatusForbidden, got, "port=%d fromIP=%q should not be blocked", c.port, c.fromIP)
				}
			}
		})
	}

	// =====================================================================
	// MaskRequestHost
	// =====================================================================

	t.Run("mask/baseline_no_mask", func(t *testing.T) { //nolint:paralleltest // sequential
		host := echoHost()
		require.NotEmpty(t, host)
		require.NotContains(t, host, "masked-host")
	})

	maskedTemplate := fmt.Sprintf("masked-host:%s", pool.MaskRequestHostPortPlaceholder)
	maskedExpected := fmt.Sprintf("masked-host:%d", echoPort)

	t.Run("mask/set_with_port_placeholder", func(t *testing.T) { //nolint:paralleltest // sequential
		updateAll(api.PutSandboxesSandboxIDNetworkJSONRequestBody{MaskRequestHost: &maskedTemplate})
		require.Equal(t, maskedExpected, echoHost())
	})

	t.Run("mask/update", func(t *testing.T) { //nolint:paralleltest // sequential
		mask := "other-host:9999"
		updateAll(api.PutSandboxesSandboxIDNetworkJSONRequestBody{MaskRequestHost: &mask})
		require.Equal(t, "other-host:9999", echoHost())
	})

	t.Run("mask/clear", func(t *testing.T) { //nolint:paralleltest // sequential
		resetRules()
		host := echoHost()
		require.NotEqual(t, "other-host:9999", host)
		require.NotContains(t, host, "masked-host")
	})

	t.Run("mask/set_again", func(t *testing.T) { //nolint:paralleltest // sequential
		updateAll(api.PutSandboxesSandboxIDNetworkJSONRequestBody{MaskRequestHost: &maskedTemplate})
		require.Equal(t, maskedExpected, echoHost())
	})

	// Clear mask before combined section.
	resetRules()

	// =====================================================================
	// Combined egress + ingress in a single PUT
	// =====================================================================

	t.Run("combined/egress_deny_and_ingress_port_deny", func(t *testing.T) { //nolint:paralleltest // sequential
		denyInPort := fmt.Sprintf("0.0.0.0/0:%d", testPort)
		updateAll(api.PutSandboxesSandboxIDNetworkJSONRequestBody{
			DenyOut: ptrS(blockAll),
			DenyIn:  ptrS(denyInPort),
		})

		// Egress: all outbound blocked.
		verifyConnectivity(t, ctx, sbx, envdClient, []connectivityCheck{
			{"https://8.8.8.8", false},
			{"https://1.1.1.1", false},
		})
		// Ingress: denied port blocked, other port still open.
		require.Equal(t, http.StatusForbidden, request(testPort, ""))
		require.NotEqual(t, http.StatusForbidden, request(testPort+1, ""))
	})

	t.Run("combined/clear_restores_both", func(t *testing.T) { //nolint:paralleltest // sequential
		resetRules()
		verifyConnectivity(t, ctx, sbx, envdClient, []connectivityCheck{
			{"https://8.8.8.8", true},
			{"https://1.1.1.1", true},
		})
		require.NotEqual(t, http.StatusForbidden, request(testPort, ""))
		require.NotEqual(t, http.StatusForbidden, request(testPort+1, ""))
	})

	t.Run("combined/egress_allow_with_ingress_deny", func(t *testing.T) { //nolint:paralleltest // sequential
		updateAll(api.PutSandboxesSandboxIDNetworkJSONRequestBody{
			AllowOut: ptrS("8.8.8.8", "google.com"),
			DenyOut:  ptrS(blockAll),
			DenyIn:   ptrS(blockAll),
		})

		// Egress: allowed IP and domain work, others blocked.
		verifyConnectivity(t, ctx, sbx, envdClient, []connectivityCheck{
			{"https://8.8.8.8", true},
			{"https://google.com", true},
			{"https://1.1.1.1", false},
			{"https://cloudflare.com", false},
		})
		// Ingress: all blocked.
		require.Equal(t, http.StatusForbidden, request(testPort, ""))
		require.Equal(t, http.StatusForbidden, request(testPort+1, ""))
	})

	t.Run("combined/clear_again", func(t *testing.T) { //nolint:paralleltest // sequential
		resetRules()
		verifyConnectivity(t, ctx, sbx, envdClient, []connectivityCheck{
			{"https://8.8.8.8", true},
			{"https://1.1.1.1", true},
		})
		require.NotEqual(t, http.StatusForbidden, request(testPort, ""))
		require.NotEqual(t, http.StatusForbidden, request(testPort+1, ""))
	})

	// =====================================================================
	// Pause/resume preserves egress, ingress, and mask (must be last)
	// =====================================================================

	t.Run("pause_resume_preserves_all", func(t *testing.T) { //nolint:paralleltest // sequential
		denyInPort := fmt.Sprintf("0.0.0.0/0:%d", testPort)
		updateAll(api.PutSandboxesSandboxIDNetworkJSONRequestBody{
			AllowOut: ptrS("8.8.8.8", "google.com"),
			DenyOut:  ptrS(blockAll),
			DenyIn:   ptrS(denyInPort),
		})

		// Verify before pause.
		verifyConnectivity(t, ctx, sbx, envdClient, []connectivityCheck{
			{"https://8.8.8.8", true},
			{"https://google.com", true},
			{"https://1.1.1.1", false},
			{"https://cloudflare.com", false},
		})
		require.Equal(t, http.StatusForbidden, request(testPort, ""))
		require.NotEqual(t, http.StatusForbidden, request(testPort+1, ""))

		// Pause.
		pauseResp, err := client.PostSandboxesSandboxIDPauseWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, pauseResp.StatusCode())

		// Resume.
		resumeResp, err := client.PostSandboxesSandboxIDResumeWithResponse(ctx, sbx.SandboxID,
			api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resumeResp.StatusCode())

		// Verify all survived.
		verifyConnectivity(t, ctx, sbx, envdClient, []connectivityCheck{
			{"https://8.8.8.8", true},
			{"https://google.com", true},
			{"https://1.1.1.1", false},
			{"https://cloudflare.com", false},
		})
		require.Equal(t, http.StatusForbidden, request(testPort, ""))
		require.NotEqual(t, http.StatusForbidden, request(testPort+1, ""))
	})
}
