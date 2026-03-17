package sandboxes

import (
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
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

// =============================================================================
// Ingress test helpers
// =============================================================================

// ingressRules is a builder for ingress config updates.
type ingressRules struct {
	body api.PutSandboxesSandboxIDNetworkJSONRequestBody
}

func ingress() *ingressRules { return &ingressRules{} }

func (r *ingressRules) denyIn(cidrs ...string) *ingressRules {
	r.body.DenyIn = &cidrs

	return r
}

func (r *ingressRules) allowIn(cidrs ...string) *ingressRules {
	r.body.AllowIn = &cidrs

	return r
}

func (r *ingressRules) denyOut(cidrs ...string) *ingressRules {
	r.body.DenyOut = &cidrs

	return r
}

func (r *ingressRules) maskHost(h string) *ingressRules {
	r.body.MaskRequestHost = &h

	return r
}

// =============================================================================
// TestNetworkIngress — single shared sandbox, all ingress + combined tests.
// =============================================================================

func TestNetworkIngress(t *testing.T) { //nolint:tparallel // subtests are sequential
	t.Parallel()

	ctx := t.Context()
	client := setup.GetAPIClient()
	envdClient := setup.GetEnvdClient(t, ctx)

	// Use the network-egress-test template so we have curl available for
	// the combined egress+ingress test at the end.
	templateID := ensureNetworkTestTemplate(t)

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(180),
		utils.WithAutoPause(false),
	)

	testPort := 8000
	proxyURL, err := url.Parse(setup.EnvdProxy)
	require.NoError(t, err)
	httpClient := &http.Client{Timeout: 10 * time.Second}

	apply := func(r *ingressRules) {
		t.Helper()
		resp := putNetwork(t, ctx, client, sbx.SandboxID, r.body)
		if resp.StatusCode() != http.StatusNoContent {
			t.Logf("PUT body: %+v", r.body)
			t.Logf("Response status: %d, body: %s", resp.StatusCode(), string(resp.Body))
		}
		require.Equal(t, http.StatusNoContent, resp.StatusCode())
	}

	proxyStatus := func(port int, fromIP string) int {
		t.Helper()
		var headers *http.Header
		if fromIP != "" {
			headers = &http.Header{"X-Forwarded-For": []string{fromIP + ", 0.0.0.0"}}
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

	// Wait for server to be reachable before running tests.
	waitResp := utils.WaitForStatus(t, httpClient, sbx, proxyURL, testPort, nil, http.StatusOK)
	waitResp.Body.Close()

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
		// ── Port deny/allow (unified format: CIDR:port) ──────────────
		{
			name:  "port_deny_blocks_access",
			rules: ingress().denyIn(fmt.Sprintf("0.0.0.0/0:%d", testPort)),
			checks: []check{
				{testPort, "", true},
				{testPort + 1, "", false},
			},
		},
		{
			name:  "port_allow_overrides_deny",
			rules: ingress().allowIn(fmt.Sprintf("0.0.0.0/0:%d", testPort)).denyIn("0.0.0.0/0"),
			checks: []check{
				{testPort, "", false},
				{testPort + 1, "", true},
			},
		},
		// ── Client IP deny/allow ─────────────────────────────────────
		{
			// Both IPv4 and IPv6 "match all" CIDRs for completeness.
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
		// ── Spoofed X-Forwarded-For (CI-only, bypass client-proxy) ───
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
		// ── Port-specific deny with CIDR (envd port is exempt from ingress) ──
		{
			name:  "envd_exempt_from_ingress_restrictions",
			rules: ingress().denyIn("0.0.0.0/0"),
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

	// ── Combined egress+ingress in a single PUT ─────────────────────────

	t.Run("combined/egress_and_ingress", func(t *testing.T) { //nolint:paralleltest // sequential
		// Single PUT: deny all egress + deny port for ingress.
		apply(ingress().denyOut(blockAll).denyIn(fmt.Sprintf("0.0.0.0/0:%d", testPort)))

		// Egress: outbound blocked.
		verifyConnectivity(t, ctx, sbx, envdClient, []connectivityCheck{
			{"https://8.8.8.8", false},
		})

		// Ingress: port blocked.
		require.Equal(t, http.StatusForbidden, proxyStatus(testPort, ""))
	})

	t.Run("combined/clear_restores_both", func(t *testing.T) { //nolint:paralleltest // sequential
		apply(ingress())

		// Egress: outbound restored.
		verifyConnectivity(t, ctx, sbx, envdClient, []connectivityCheck{
			{"https://8.8.8.8", true},
		})

		// Ingress: port restored.
		require.NotEqual(t, http.StatusForbidden, proxyStatus(testPort, ""))
	})

	// ── Pause/resume preserves ingress rules (must be last) ─────────────

	t.Run("pause_resume_preserves_ingress_rules", func(t *testing.T) { //nolint:paralleltest // sequential
		apply(ingress().denyIn(fmt.Sprintf("0.0.0.0/0:%d", testPort)))
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
// TestNetworkMaskRequestHost exercises dynamic MaskRequestHost updates.
// A Python server echoes the Host header back so we can verify masking.
// =============================================================================

func TestNetworkMaskRequestHost(t *testing.T) { //nolint:tparallel // subtests are sequential
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
