package sandboxes

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
	sharedutils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

// =============================================================================
// Shared helpers and template setup
// =============================================================================

var (
	networkTestTemplateID   string
	networkTestTemplateOnce sync.Once
)

const blockAll = sandbox_network.AllInternetTrafficCIDR

func ptrS(s ...string) *[]string { return &s }

// ensureNetworkTestTemplate builds the custom template for network tests (called once).
func ensureNetworkTestTemplate(t *testing.T) string {
	t.Helper()

	networkTestTemplateOnce.Do(func() {
		t.Log("Building custom template for network egress tests...")

		template := utils.BuildTemplate(t, utils.TemplateBuildOptions{
			Name: "network-egress-test",
			BuildData: api.TemplateBuildStartV2{
				FromImage: sharedutils.ToPtr("ubuntu:22.04"),
				Steps: sharedutils.ToPtr([]api.TemplateStep{
					{
						Type: "RUN",
						Args: sharedutils.ToPtr([]string{"sudo apt-get update && sudo apt-get install -y curl iputils-ping dnsutils openssh-client gnupg && sudo rm -rf /var/lib/apt/lists/*"}),
					},
				}),
			},
			LogHandler:  utils.DefaultBuildLogHandler(t),
			ReqEditors:  []api.RequestEditorFn{setup.WithAPIKey()},
			EnableDebug: false,
		})

		networkTestTemplateID = template.TemplateID
		t.Logf("Network test template built: %s", networkTestTemplateID)
	})

	if networkTestTemplateID == "" {
		t.Fatal("Network test template was not built successfully")
	}

	return networkTestTemplateID
}

// putNetwork calls the update network endpoint.
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

// requireTCPAllowed asserts that an HTTP/HTTPS request succeeds.
func requireTCPAllowed(t *testing.T, ctx context.Context, sbx *api.Sandbox, envdClient *setup.EnvdClient, url, msg string) {
	t.Helper()
	err := utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "5", "--max-time", "10", "-Iks", url)
	require.NoError(t, err, msg)
}

// requireTCPBlocked asserts that an HTTP/HTTPS request is blocked.
// RES_OPTIONS caps glibc DNS timeout so blocked-domain curls fail in ~2 s
// instead of curl's default ~20 s DNS retry cycle.
func requireTCPBlocked(t *testing.T, ctx context.Context, sbx *api.Sandbox, envdClient *setup.EnvdClient, url, msg string) {
	t.Helper()
	err := utils.ExecCommand(t, ctx, sbx, envdClient,
		"sh", "-c", `RES_OPTIONS="timeout:1 attempts:1" curl --connect-timeout 0.3 --max-time 0.5 -Iks `+url)
	require.Error(t, err, msg)
}

// requireDNSAllowed asserts that a UDP DNS query to 8.8.8.8 succeeds.
func requireDNSAllowed(t *testing.T, ctx context.Context, sbx *api.Sandbox, envdClient *setup.EnvdClient, msg string) {
	t.Helper()
	err := utils.ExecCommand(t, ctx, sbx, envdClient, "dig", "+short", "@8.8.8.8", "google.com")
	require.NoError(t, err, msg)
}

// requireDNSBlocked asserts that a UDP DNS query to the given server is blocked.
func requireDNSBlocked(t *testing.T, ctx context.Context, sbx *api.Sandbox, envdClient *setup.EnvdClient, server, msg string) {
	t.Helper()
	err := utils.ExecCommand(t, ctx, sbx, envdClient, "dig", "+short", "+timeout=1", "+retry=0", fmt.Sprintf("@%s", server), "google.com")
	require.Error(t, err, msg)
}

// assertHTTPResponseFromServer asserts that an HTTPS request returns a response from the expected server.
func assertHTTPResponseFromServer(t *testing.T, ctx context.Context, sbx *api.Sandbox, envdClient *setup.EnvdClient, url, expectedServerHeader, msg string) {
	t.Helper()
	output, err := utils.ExecCommandWithOutput(t, ctx, sbx, envdClient, nil, "user", "curl", "--connect-timeout", "5", "--max-time", "10", "-Iks", url)
	require.NoError(t, err, msg)
	require.Contains(t, strings.ToLower(output), strings.ToLower(expectedServerHeader),
		"%s - expected server header to contain %q, got response: %s", msg, expectedServerHeader, output)
}

type connectivityCheck struct {
	url     string
	allowed bool
}

func verifyConnectivity(t *testing.T, ctx context.Context, sbx *api.Sandbox, envdClient *setup.EnvdClient, checks []connectivityCheck) {
	t.Helper()
	for _, c := range checks {
		if c.allowed {
			requireTCPAllowed(t, ctx, sbx, envdClient, c.url, c.url+" should be reachable")
		} else {
			requireTCPBlocked(t, ctx, sbx, envdClient, c.url, c.url+" should be blocked")
		}
	}
}

// =============================================================================
// TestNetworkEgress — single shared sandbox, all egress tests sequential.
// =============================================================================

func TestNetworkEgress(t *testing.T) { //nolint:tparallel // subtests are sequential
	t.Parallel()

	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()
	envdClient := setup.GetEnvdClient(t, ctx)

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(300),
		utils.WithAutoPause(false),
	)

	update := func(allow, deny []string) {
		t.Helper()
		var a, d *[]string
		if allow != nil {
			a = &allow
		}
		if deny != nil {
			d = &deny
		}
		resp := putNetwork(t, ctx, client, sbx.SandboxID, api.PutSandboxesSandboxIDNetworkJSONRequestBody{
			AllowOut: a,
			DenyOut:  d,
		})
		require.Equal(t, http.StatusNoContent, resp.StatusCode())
	}

	resetRules := func() {
		t.Helper()
		update(nil, nil)
	}

	// ── API validation: error responses ──────────────────────────────────

	t.Run("api/not_found", func(t *testing.T) { //nolint:paralleltest // sequential
		resp := putNetwork(t, ctx, client, "ixxxxxxxxxxxxxxxxxx0",
			api.PutSandboxesSandboxIDNetworkJSONRequestBody{AllowOut: ptrS("8.8.8.8")},
		)
		require.Equal(t, http.StatusNotFound, resp.StatusCode())
	})

	t.Run("api/unauthorized", func(t *testing.T) { //nolint:paralleltest // sequential
		resp, err := client.PutSandboxesSandboxIDNetworkWithResponse(
			ctx, "any-sandbox-id", api.PutSandboxesSandboxIDNetworkJSONRequestBody{},
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode())
	})

	// ── API validation: rejected (400) ──────────────────────────────────

	rejectedCases := []struct {
		name     string
		allowOut *[]string
		denyOut  *[]string
	}{
		{"domain_in_deny_out", nil, ptrS("example.com")},
		{"garbage_in_deny_out", nil, ptrS("not-a-cidr")},
		{"domain_in_deny_out_alongside_block_all", nil, ptrS(blockAll, "example.com")},
		{"domain_allow_without_deny", ptrS("google.com"), nil},
		{"domain_allow_with_partial_deny", ptrS("google.com"), ptrS("10.0.0.0/8")},
		{"wildcard_domain_without_deny_all", ptrS("*.example.com"), nil},
		{"wildcard_domain_with_partial_deny", ptrS("*.example.com"), ptrS("10.0.0.0/8")},
		{"mixed_domain_ip_without_deny_all", ptrS("example.com", "8.8.8.8"), ptrS("10.0.0.0/8")},
	}
	for _, tc := range rejectedCases {
		t.Run("api/reject/"+tc.name, func(t *testing.T) { //nolint:paralleltest // sequential
			resp := putNetwork(t, ctx, client, sbx.SandboxID, api.PutSandboxesSandboxIDNetworkJSONRequestBody{
				AllowOut: tc.allowOut,
				DenyOut:  tc.denyOut,
			})
			require.Equal(t, http.StatusBadRequest, resp.StatusCode())
		})
	}

	// ── API validation: accepted (204) ──────────────────────────────────

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
		t.Run("api/accept/"+tc.name, func(t *testing.T) { //nolint:paralleltest // sequential
			resp := putNetwork(t, ctx, client, sbx.SandboxID, api.PutSandboxesSandboxIDNetworkJSONRequestBody{
				AllowOut: tc.allowOut,
				DenyOut:  tc.denyOut,
			})
			require.Equal(t, http.StatusNoContent, resp.StatusCode())
		})
	}

	// ── IP/CIDR filtering ───────────────────────────────────────────────

	t.Run("ip/allow_specific", func(t *testing.T) { //nolint:paralleltest // sequential
		update([]string{"8.8.8.8"}, []string{blockAll})
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://8.8.8.8", "allowed IP reachable")
		requireTCPBlocked(t, ctx, sbx, envdClient, "https://1.1.1.1", "non-allowed IP blocked")
	})

	t.Run("ip/block_specific", func(t *testing.T) { //nolint:paralleltest // sequential
		update(nil, []string{"8.8.8.8"})
		requireTCPBlocked(t, ctx, sbx, envdClient, "https://8.8.8.8", "denied IP blocked")
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://1.1.1.1", "non-denied IP allowed")
	})

	t.Run("ip/allow_cidr", func(t *testing.T) { //nolint:paralleltest // sequential
		update([]string{"8.8.8.0/24"}, []string{blockAll})
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://8.8.8.8", "IP in allowed CIDR")
		requireTCPBlocked(t, ctx, sbx, envdClient, "https://1.1.1.1", "IP outside CIDR blocked")
	})

	t.Run("ip/block_cidr", func(t *testing.T) { //nolint:paralleltest // sequential
		update(nil, []string{"8.8.8.0/24"})
		requireTCPBlocked(t, ctx, sbx, envdClient, "https://8.8.8.8", "IP in denied CIDR blocked")
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://1.1.1.1", "IP outside CIDR allowed")
	})

	t.Run("ip/allow_overrides_block", func(t *testing.T) { //nolint:paralleltest // sequential
		update([]string{"8.8.8.8"}, []string{"8.8.8.0/24"})
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://8.8.8.8", "allow takes precedence")
	})

	t.Run("ip/partial_allow_deny_default_allow", func(t *testing.T) { //nolint:paralleltest // sequential
		update([]string{"8.8.8.8"}, []string{"1.1.1.1"})
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://8.8.8.8", "explicitly allowed")
		requireTCPBlocked(t, ctx, sbx, envdClient, "https://1.1.1.1", "explicitly denied")
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://1.0.0.1", "default allow for unmatched")
	})

	t.Run("ip/multiple_allowed", func(t *testing.T) { //nolint:paralleltest // sequential
		update([]string{"8.8.8.8", "1.1.1.1"}, []string{blockAll})
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://8.8.8.8", "first allowed IP")
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://1.1.1.1", "second allowed IP")
	})

	t.Run("ip/allow_all_cidr", func(t *testing.T) { //nolint:paralleltest // sequential
		update([]string{blockAll}, []string{blockAll})
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://8.8.8.8", "0.0.0.0/0 allow overrides deny")
	})

	t.Run("ip/empty_config_allows_all", func(t *testing.T) { //nolint:paralleltest // sequential
		resetRules()
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://8.8.8.8", "reachable with no rules")
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://1.1.1.1", "reachable with no rules")
	})

	t.Run("ip/private_ranges_always_blocked", func(t *testing.T) { //nolint:paralleltest // sequential
		for _, pr := range []struct{ cidr, ip, desc string }{
			{"10.0.0.0/8", "10.0.0.1", "10/8"},
			{"192.168.0.0/16", "192.168.0.1", "192.168/16"},
			{"172.16.0.0/12", "172.16.0.1", "172.16/12"},
			{"169.254.0.0/16", "169.254.0.1", "169.254/16 (link-local)"},
		} {
			update([]string{pr.cidr}, nil)
			requireTCPBlocked(t, ctx, sbx, envdClient, pr.ip, fmt.Sprintf("private IP %s always blocked", pr.desc))
		}
	})

	// ── Domain filtering ────────────────────────────────────────────────

	t.Run("domain/allow_through_blocked_internet", func(t *testing.T) { //nolint:paralleltest // sequential
		update([]string{"google.com"}, []string{blockAll})
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://google.com", "allowed domain reachable")
		requireTCPBlocked(t, ctx, sbx, envdClient, "https://cloudflare.com", "non-allowed domain blocked")
	})

	t.Run("domain/wildcard", func(t *testing.T) { //nolint:paralleltest // sequential
		update([]string{"*.google.com"}, []string{blockAll})
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://www.google.com", "subdomain matches wildcard")
		requireTCPBlocked(t, ctx, sbx, envdClient, "https://cloudflare.com", "non-matching blocked")
	})

	t.Run("domain/exact_vs_subdomain", func(t *testing.T) { //nolint:paralleltest // sequential
		update([]string{"google.com"}, []string{blockAll})
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://google.com", "exact match")
		requireTCPBlocked(t, ctx, sbx, envdClient, "https://mail.google.com", "subdomain not matched by exact rule")
	})

	t.Run("domain/wildcard_star", func(t *testing.T) { //nolint:paralleltest // sequential
		update([]string{"*"}, []string{blockAll})
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://google.com", "* matches any domain")
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://github.com", "* matches any domain")
	})

	t.Run("domain/case_insensitive", func(t *testing.T) { //nolint:paralleltest // sequential
		update([]string{"Google.Com"}, []string{blockAll})
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://google.com", "case insensitive")
		requireTCPBlocked(t, ctx, sbx, envdClient, "https://cloudflare.com", "non-matching blocked")
	})

	t.Run("domain/and_ip_combined", func(t *testing.T) { //nolint:paralleltest // sequential
		update([]string{"google.com", "1.1.1.1"}, []string{blockAll})
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://google.com", "domain allowed")
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://1.1.1.1", "IP allowed")
		requireTCPBlocked(t, ctx, sbx, envdClient, "https://cloudflare.com", "non-allowed domain blocked")
	})

	t.Run("domain/https_by_ip_no_hostname", func(t *testing.T) { //nolint:paralleltest // sequential
		update([]string{"8.8.8.8"}, []string{blockAll})
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://8.8.8.8", "HTTPS by IP uses CIDR rule")
		requireTCPBlocked(t, ctx, sbx, envdClient, "https://1.1.1.1", "non-allowed IP blocked")
	})

	t.Run("domain/http_host_header", func(t *testing.T) { //nolint:paralleltest // sequential
		update([]string{"google.com"}, []string{blockAll})
		requireTCPAllowed(t, ctx, sbx, envdClient, "http://google.com", "HTTP domain via Host header")
		requireTCPBlocked(t, ctx, sbx, envdClient, "http://cloudflare.com", "non-allowed HTTP domain blocked")
	})

	// ── Port rules ──────────────────────────────────────────────────────

	t.Run("port/ip_single", func(t *testing.T) { //nolint:paralleltest // sequential
		update([]string{"8.8.8.8:443"}, []string{blockAll})
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://8.8.8.8", "HTTPS 8.8.8.8:443 allowed")
		requireDNSBlocked(t, ctx, sbx, envdClient, "8.8.8.8", "DNS 8.8.8.8:53 blocked (port not allowed)")
	})

	t.Run("port/udp_only", func(t *testing.T) { //nolint:paralleltest // sequential
		update([]string{"8.8.8.8:53"}, []string{blockAll})
		requireDNSAllowed(t, ctx, sbx, envdClient, "DNS 8.8.8.8:53 allowed")
		requireTCPBlocked(t, ctx, sbx, envdClient, "https://8.8.8.8", "HTTPS blocked (only :53)")
	})

	t.Run("port/range", func(t *testing.T) { //nolint:paralleltest // sequential
		update([]string{"8.8.8.8:53-443"}, []string{blockAll})
		requireDNSAllowed(t, ctx, sbx, envdClient, "DNS 8.8.8.8:53 in range")
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://8.8.8.8", "HTTPS 8.8.8.8:443 in range")
	})

	t.Run("port/cidr_with_port", func(t *testing.T) { //nolint:paralleltest // sequential
		update([]string{"8.8.8.0/24:443"}, []string{blockAll})
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://8.8.8.8", "CIDR+port allowed")
		requireDNSBlocked(t, ctx, sbx, envdClient, "8.8.8.8", "DNS blocked (port not in rule)")
		requireTCPBlocked(t, ctx, sbx, envdClient, "https://1.1.1.1", "IP not in CIDR blocked")
	})

	t.Run("port/deny_specific", func(t *testing.T) { //nolint:paralleltest // sequential
		update(nil, []string{"8.8.8.8:443"})
		requireDNSAllowed(t, ctx, sbx, envdClient, "DNS 8.8.8.8:53 allowed (port not denied)")
		requireTCPBlocked(t, ctx, sbx, envdClient, "https://8.8.8.8", "HTTPS 8.8.8.8:443 denied")
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://1.1.1.1", "other IP allowed")
	})

	t.Run("port/domain_with_port", func(t *testing.T) { //nolint:paralleltest // sequential
		update([]string{"google.com:443"}, []string{blockAll})
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://google.com", "HTTPS google.com:443")
		requireTCPBlocked(t, ctx, sbx, envdClient, "http://google.com", "HTTP :80 blocked (only 443)")
	})

	t.Run("port/multiple_ips_different_ports", func(t *testing.T) { //nolint:paralleltest // sequential
		update([]string{"8.8.8.8:53", "1.1.1.1:443"}, []string{blockAll})
		requireDNSAllowed(t, ctx, sbx, envdClient, "DNS 8.8.8.8:53 allowed")
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://1.1.1.1", "HTTPS 1.1.1.1:443 allowed")
		requireTCPBlocked(t, ctx, sbx, envdClient, "https://8.8.8.8", "HTTPS 8.8.8.8:443 blocked (only :53)")
		requireDNSBlocked(t, ctx, sbx, envdClient, "1.1.1.1", "DNS 1.1.1.1:53 blocked (only :443)")
	})

	t.Run("port/mix_port_and_allport", func(t *testing.T) { //nolint:paralleltest // sequential
		update([]string{"8.8.8.8", "1.1.1.1:443"}, []string{blockAll})
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://8.8.8.8", "8.8.8.8 all ports")
		requireDNSAllowed(t, ctx, sbx, envdClient, "DNS 8.8.8.8 all ports")
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://1.1.1.1", "1.1.1.1:443 allowed")
		requireDNSBlocked(t, ctx, sbx, envdClient, "1.1.1.1", "1.1.1.1:53 blocked (only :443)")
	})

	t.Run("port/allow_overrides_deny", func(t *testing.T) { //nolint:paralleltest // sequential
		update([]string{"8.8.8.8:443"}, []string{"8.8.8.8"})
		requireTCPAllowed(t, ctx, sbx, envdClient, "https://8.8.8.8", "allow:443 > deny all ports")
		requireDNSBlocked(t, ctx, sbx, envdClient, "8.8.8.8", "DNS blocked (not in allow, caught by deny)")
	})

	// ── UDP (DNS) ───────────────────────────────────────────────────────

	t.Run("udp/allowed_ip", func(t *testing.T) { //nolint:paralleltest // sequential
		update([]string{"8.8.8.8"}, []string{blockAll})
		requireDNSAllowed(t, ctx, sbx, envdClient, "DNS to allowed IP 8.8.8.8")
		requireDNSBlocked(t, ctx, sbx, envdClient, "1.1.1.1", "DNS to non-allowed IP 1.1.1.1")
	})

	t.Run("udp/allowed_cidr", func(t *testing.T) { //nolint:paralleltest // sequential
		update([]string{"8.8.8.0/24"}, []string{blockAll})
		requireDNSAllowed(t, ctx, sbx, envdClient, "DNS to IP in allowed CIDR")
		requireDNSBlocked(t, ctx, sbx, envdClient, "1.1.1.1", "DNS to IP outside CIDR")
	})

	// ── Sequential update lifecycle ─────────────────────────────────────

	type step struct {
		name     string
		allowOut *[]string
		denyOut  *[]string
		checks   []connectivityCheck
	}

	steps := []step{
		{
			name:    "lifecycle/1_deny_all",
			denyOut: ptrS(blockAll),
			checks:  []connectivityCheck{{"https://8.8.8.8", false}, {"https://1.1.1.1", false}},
		},
		{
			name:     "lifecycle/2_allow_ip_through_deny",
			allowOut: ptrS("8.8.8.8"), denyOut: ptrS(blockAll),
			checks: []connectivityCheck{{"https://8.8.8.8", true}, {"https://1.1.1.1", false}},
		},
		{
			name:     "lifecycle/3_replace_allowed_ip",
			allowOut: ptrS("1.1.1.1"), denyOut: ptrS(blockAll),
			checks: []connectivityCheck{{"https://1.1.1.1", true}, {"https://8.8.8.8", false}},
		},
		{
			name:     "lifecycle/4_allow_multiple",
			allowOut: ptrS("8.8.8.8", "1.1.1.1"), denyOut: ptrS(blockAll),
			checks: []connectivityCheck{{"https://8.8.8.8", true}, {"https://1.1.1.1", true}},
		},
		{
			name:     "lifecycle/5_allow_cidr",
			allowOut: ptrS("8.8.8.0/24"), denyOut: ptrS(blockAll),
			checks: []connectivityCheck{{"https://8.8.8.8", true}, {"https://1.1.1.1", false}},
		},
		{
			name:     "lifecycle/6_allow_domain",
			allowOut: ptrS("google.com"), denyOut: ptrS(blockAll),
			checks: []connectivityCheck{{"https://google.com", true}, {"https://cloudflare.com", false}},
		},
		{
			name:     "lifecycle/7_allow_domain_and_ip",
			allowOut: ptrS("google.com", "1.1.1.1"), denyOut: ptrS(blockAll),
			checks: []connectivityCheck{{"https://google.com", true}, {"https://1.1.1.1", true}, {"https://cloudflare.com", false}},
		},
		{
			name:    "lifecycle/8_remove_allow_keep_deny",
			denyOut: ptrS(blockAll),
			checks:  []connectivityCheck{{"https://google.com", false}, {"https://8.8.8.8", false}},
		},
		{
			name:   "lifecycle/9_clear_restores_access",
			checks: []connectivityCheck{{"https://8.8.8.8", true}, {"https://1.1.1.1", true}},
		},
		{
			name:     "lifecycle/10_reapply_after_clear",
			allowOut: ptrS("1.1.1.1"), denyOut: ptrS(blockAll),
			checks: []connectivityCheck{{"https://1.1.1.1", true}, {"https://8.8.8.8", false}},
		},
		{
			name:     "lifecycle/11_allow_without_deny",
			allowOut: ptrS("8.8.8.8"),
			checks:   []connectivityCheck{{"https://8.8.8.8", true}, {"https://1.1.1.1", true}},
		},
		{
			name:   "lifecycle/12_final_clear",
			checks: []connectivityCheck{{"https://8.8.8.8", true}, {"https://1.1.1.1", true}},
		},
	}

	for _, s := range steps { //nolint:paralleltest // sequential
		t.Run(s.name, func(t *testing.T) {
			resp := putNetwork(t, ctx, client, sbx.SandboxID, api.PutSandboxesSandboxIDNetworkJSONRequestBody{
				AllowOut: s.allowOut,
				DenyOut:  s.denyOut,
			})
			require.Equal(t, http.StatusNoContent, resp.StatusCode())
			verifyConnectivity(t, ctx, sbx, envdClient, s.checks)
		})
	}

	// ── Non-HTTP protocols ──────────────────────────────────────────────

	t.Run("proto/ssh_no_config", func(t *testing.T) { //nolint:paralleltest // sequential
		resetRules()
		output, err := utils.ExecCommandWithOutput(t, ctx, sbx, envdClient, nil, "user",
			"ssh", "-T", "-o", "StrictHostKeyChecking=accept-new",
			"-o", "ConnectTimeout=5", "git@github.com")
		require.Error(t, err, "SSH exits non-zero (no credentials)")
		require.Contains(t, output, "Permission denied (publickey)")
	})

	t.Run("proto/ssh_with_config", func(t *testing.T) { //nolint:paralleltest // sequential
		update([]string{blockAll}, []string{blockAll})
		output, err := utils.ExecCommandWithOutput(t, ctx, sbx, envdClient, nil, "user",
			"ssh", "-T", "-o", "StrictHostKeyChecking=accept-new",
			"-o", "ConnectTimeout=5", "git@github.com")
		require.Error(t, err, "SSH exits non-zero (no credentials)")
		require.Contains(t, output, "Permission denied (publickey)")
	})

	t.Run("proto/gpg_keyserver_no_config", func(t *testing.T) { //nolint:paralleltest // sequential
		resetRules()
		output, err := utils.ExecCommandWithOutput(t, ctx, sbx, envdClient, nil, "user",
			"gpg", "--keyserver", "hkp://keyserver.ubuntu.com",
			"--recv-key", "95C0FAF38DB3CCAD0C080A7BDC78B2DDEABC47B7")
		require.NoError(t, err, "GPG keyserver should succeed, output: %s", output)
	})

	t.Run("proto/gpg_keyserver_with_config", func(t *testing.T) { //nolint:paralleltest // sequential
		update([]string{blockAll}, []string{blockAll})
		output, err := utils.ExecCommandWithOutput(t, ctx, sbx, envdClient, nil, "user",
			"gpg", "--keyserver", "hkp://keyserver.ubuntu.com",
			"--recv-key", "95C0FAF38DB3CCAD0C080A7BDC78B2DDEABC47B7")
		require.NoError(t, err, "GPG keyserver should succeed, output: %s", output)
	})

	// ── Pause/resume (last — changes sandbox lifecycle) ─────────────────

	t.Run("persistence/pause_resume_update_rules", func(t *testing.T) { //nolint:paralleltest // sequential
		update([]string{"8.8.8.8"}, []string{blockAll})
		verifyConnectivity(t, ctx, sbx, envdClient, []connectivityCheck{
			{"https://8.8.8.8", true},
			{"https://1.1.1.1", false},
		})

		pauseResp, err := client.PostSandboxesSandboxIDPauseWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, pauseResp.StatusCode())

		resumeResp, err := client.PostSandboxesSandboxIDResumeWithResponse(ctx, sbx.SandboxID,
			api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
		require.NoError(t, err)
		require.Equal(t, http.StatusCreated, resumeResp.StatusCode())

		verifyConnectivity(t, ctx, sbx, envdClient, []connectivityCheck{
			{"https://8.8.8.8", true},
			{"https://1.1.1.1", false},
		})
	})
}

// =============================================================================
// Tests requiring dedicated sandboxes (destructive or creation-time config)
// =============================================================================

// TestNetworkEgressPersistIP tests that IP-based rules survive pause/resume.
func TestNetworkEgressPersistIP(t *testing.T) {
	t.Parallel()

	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()
	envdClient := setup.GetEnvdClient(t, ctx)

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &[]string{"8.8.8.8"},
			DenyOut:  &[]string{blockAll},
		}),
	)

	requireTCPAllowed(t, ctx, sbx, envdClient, "https://8.8.8.8", "before pause")
	requireTCPBlocked(t, ctx, sbx, envdClient, "https://1.1.1.1", "before pause")

	respPause, err := client.PostSandboxesSandboxIDPauseWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, respPause.StatusCode())

	respResume, err := client.PostSandboxesSandboxIDResumeWithResponse(ctx, sbx.SandboxID,
		api.PostSandboxesSandboxIDResumeJSONRequestBody{Timeout: sharedutils.ToPtr(int32(60))}, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, respResume.StatusCode())

	requireTCPAllowed(t, ctx, sbx, envdClient, "https://8.8.8.8", "after resume")
	requireTCPBlocked(t, ctx, sbx, envdClient, "https://1.1.1.1", "after resume")
}

// TestNetworkEgressPersistDomain tests that domain-based rules survive pause/resume.
func TestNetworkEgressPersistDomain(t *testing.T) {
	t.Parallel()

	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()
	envdClient := setup.GetEnvdClient(t, ctx)

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &[]string{"google.com"},
			DenyOut:  &[]string{blockAll},
		}),
	)

	requireTCPAllowed(t, ctx, sbx, envdClient, "https://google.com", "before pause")
	requireTCPBlocked(t, ctx, sbx, envdClient, "https://cloudflare.com", "before pause")

	respPause, err := client.PostSandboxesSandboxIDPauseWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, respPause.StatusCode())

	respResume, err := client.PostSandboxesSandboxIDResumeWithResponse(ctx, sbx.SandboxID,
		api.PostSandboxesSandboxIDResumeJSONRequestBody{Timeout: sharedutils.ToPtr(int32(60))}, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, respResume.StatusCode())

	requireTCPAllowed(t, ctx, sbx, envdClient, "https://google.com", "after resume")
	requireTCPBlocked(t, ctx, sbx, envdClient, "https://cloudflare.com", "after resume")
}

// TestNetworkEgressInternetAccessFalse tests AllowInternetAccess=false creation-time flag.
func TestNetworkEgressInternetAccessFalse(t *testing.T) {
	t.Parallel()

	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()
	envdClient := setup.GetEnvdClient(t, ctx)

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithAllowInternetAccess(false),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &[]string{"8.8.8.8"},
		}),
	)

	requireTCPAllowed(t, ctx, sbx, envdClient, "https://8.8.8.8", "allowed IP reachable despite AllowInternetAccess=false")
	requireTCPBlocked(t, ctx, sbx, envdClient, "https://1.1.1.1", "blocked by AllowInternetAccess=false")
}

// TestNetworkEgressDNSSpoofing tests that DNS spoofing attacks are neutralized.
func TestNetworkEgressDNSSpoofing(t *testing.T) {
	t.Parallel()

	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()
	envdClient := setup.GetEnvdClient(t, ctx)

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &[]string{"google.com"},
			DenyOut:  &[]string{blockAll},
		}),
	)

	assertHTTPResponseFromServer(t, ctx, sbx, envdClient,
		"https://google.com", "server: gws",
		"google.com returns Google server before spoofing")

	err := utils.ExecCommandAsRoot(t, ctx, sbx, envdClient, "sh", "-c", "echo '1.1.1.1 google.com' >> /etc/hosts")
	require.NoError(t, err, "modify /etc/hosts")

	assertHTTPResponseFromServer(t, ctx, sbx, envdClient,
		"https://google.com", "server: gws",
		"DNS spoofing neutralized — still returns Google server")
}
