package sandboxes

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
	sharedutils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

var (
	networkTestTemplateID   string
	networkTestTemplateOnce sync.Once
)

// ensureNetworkTestTemplate builds the custom template for network tests (called once)
func ensureNetworkTestTemplate(t *testing.T) string {
	t.Helper()

	networkTestTemplateOnce.Do(func() {
		t.Log("Building custom template for network egress tests...")

		template := utils.BuildTemplate(t, utils.TemplateBuildOptions{
			Alias: "network-egress-test",
			BuildData: api.TemplateBuildStartV2{
				FromImage: sharedutils.ToPtr("ubuntu:22.04"),
				Steps: sharedutils.ToPtr([]api.TemplateStep{
					{
						Type: "RUN",
						Args: sharedutils.ToPtr([]string{"sudo apt-get update && sudo apt-get install -y curl iputils-ping dnsutils && sudo rm -rf /var/lib/apt/lists/*"}),
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

// =============================================================================
// Test helper functions for network egress assertions
// =============================================================================

// assertHTTPRequest asserts that an HTTP/HTTPS request to the given URL succeeds or fails based on expectSuccess
func assertHTTPRequest(t *testing.T, ctx context.Context, sbx *api.Sandbox, envdClient *setup.EnvdClient, url string, expectSuccess bool, msg string) {
	t.Helper()
	connectTimeout, maxTime := "3", "5"
	if expectSuccess {
		connectTimeout, maxTime = "5", "10"
	}
	err := utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", connectTimeout, "--max-time", maxTime, "-Iks", url)
	if expectSuccess {
		require.NoError(t, err, msg)
	} else {
		require.Error(t, err, msg)
		require.Contains(t, err.Error(), "failed with exit code", "Expected connection failure message")
	}
}

// assertDNSQuery asserts that a DNS query to the given server succeeds or fails based on expectSuccess
func assertDNSQuery(t *testing.T, ctx context.Context, sbx *api.Sandbox, envdClient *setup.EnvdClient, dnsServer, domain string, expectSuccess bool, msg string) {
	t.Helper()
	err := utils.ExecCommand(t, ctx, sbx, envdClient, "dig", "+short", "+timeout=3", fmt.Sprintf("@%s", dnsServer), domain)
	if expectSuccess {
		require.NoError(t, err, msg)
	} else {
		require.Error(t, err, msg)
	}
}

// =============================================================================
// IP and CIDR-based filtering tests
// =============================================================================

// TestEgressFirewallAllowSpecificIP tests that only allowed IPs can be accessed
func TestEgressFirewallAllowSpecificIP(t *testing.T) {
	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Use Google's DNS IP (8.8.8.8) as allowed, and Cloudflare's (1.1.1.1) as implicitly blocked
	allowedIPs := []string{"8.8.8.8"}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowedIPs,
			DenyOut:  &[]string{sandbox_network.AllInternetTrafficCIDR},
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	assertHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.8.8", true, "Expected curl to allowed IP 8.8.8.8 to succeed")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://1.1.1.1", false, "Expected curl to non-allowed IP 1.1.1.1 to fail")
}

// TestEgressFirewallBlockSpecificIP tests that blocked IPs cannot be accessed
func TestEgressFirewallBlockSpecificIP(t *testing.T) {
	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Block Cloudflare's DNS IP
	blockedIPs := []string{"1.1.1.1"}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			DenyOut: &blockedIPs,
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	assertHTTPRequest(t, ctx, sbx, envdClient, "https://1.1.1.1", false, "Expected curl to blocked IP 1.1.1.1 to fail")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.8.8", true, "Expected curl to non-blocked IP 8.8.8.8 to succeed")
}

// TestEgressFirewallAllowCIDRRange tests that CIDR ranges work for allowing IPs
func TestEgressFirewallAllowCIDRRange(t *testing.T) {
	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Allow Cloudflare's range (1.1.1.0/24) which includes 1.1.1.1
	allowedRanges := []string{"1.1.1.0/24"}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowedRanges,
			DenyOut:  &[]string{sandbox_network.AllInternetTrafficCIDR},
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	assertHTTPRequest(t, ctx, sbx, envdClient, "https://1.1.1.1", true, "Expected curl to IP within allowed CIDR range (1.1.1.1) to succeed")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.8.8", false, "Expected curl to IP outside allowed CIDR range (8.8.8.8) to fail")
}

// TestEgressFirewallBlockCIDRRange tests that CIDR ranges work for blocking IPs
func TestEgressFirewallBlockCIDRRange(t *testing.T) {
	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Block Cloudflare's DNS range (1.1.1.0/24)
	blockedRanges := []string{"1.1.1.0/24"}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			DenyOut: &blockedRanges,
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	assertHTTPRequest(t, ctx, sbx, envdClient, "https://1.1.1.1", false, "Expected curl to IP within blocked CIDR range (1.1.1.1) to fail")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://1.1.1.2", false, "Expected curl to IP within blocked CIDR range (1.1.1.2) to fail")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.8.8", true, "Expected curl to IP outside blocked CIDR range (8.8.8.8) to succeed")
}

// TestEgressFirewallAllowAndBlockCombination tests that allowOut takes precedence over blockOut
func TestEgressFirewallAllowAndBlockCombination(t *testing.T) {
	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Block all internet but explicitly allow one IP - allowOut should take precedence
	allowedIPs := []string{"8.8.8.8"}
	blockAll := []string{sandbox_network.AllInternetTrafficCIDR}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowedIPs,
			DenyOut:  &blockAll,
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	assertHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.8.8", true, "Expected curl to explicitly allowed IP (8.8.8.8) to succeed even though 0.0.0.0/0 is blocked")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://1.1.1.1", false, "Expected curl to non-allowed IP (1.1.1.1) to fail")
}

// TestEgressFirewallPersistsAfterResume tests that network config persists after pause/resume
func TestEgressFirewallPersistsAfterResume(t *testing.T) {
	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	sbxTimeout := int32(60)
	client := setup.GetAPIClient()

	// Allow only specific IPs
	allowedIPs := []string{"8.8.8.8", "8.8.4.4"}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowedIPs,
			DenyOut:  &[]string{sandbox_network.AllInternetTrafficCIDR},
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	// Test before pause
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.8.8", true, "Expected curl to allowed IP (8.8.8.8) to succeed before pause")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://1.1.1.1", false, "Expected curl to non-allowed IP (1.1.1.1) to fail before pause")

	// Pause the sandbox
	respPause, err := client.PostSandboxesSandboxIDPauseWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err, "Expected to pause sandbox without error")
	require.Equal(t, http.StatusNoContent, respPause.StatusCode(), "Expected status code 204 No Content, got %d", respPause.StatusCode())

	// Resume the sandbox
	respResume, err := client.PostSandboxesSandboxIDResumeWithResponse(ctx, sbx.SandboxID, api.PostSandboxesSandboxIDResumeJSONRequestBody{
		Timeout: &sbxTimeout,
	}, setup.WithAPIKey())
	require.NoError(t, err, "Expected to resume sandbox without error")
	require.Equal(t, http.StatusCreated, respResume.StatusCode(), "Expected status code 201 Created, got %d", respResume.StatusCode())

	// Test after resume
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.8.8", true, "Expected curl to allowed IP (8.8.8.8) to succeed after resume")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://1.1.1.1", false, "Expected curl to non-allowed IP (1.1.1.1) to fail after resume")
}

// TestEgressFirewallEmptyConfig tests that empty allowOut list is treated as no restriction
func TestEgressFirewallEmptyConfig(t *testing.T) {
	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Create sandbox with empty allowOut list - should be treated as no restriction
	emptyAllowList := []string{}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &emptyAllowList,
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	assertHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.8.8", true, "Expected curl to succeed with empty allowOut list")
}

// TestEgressFirewallAllowAll tests that 0.0.0.0/0 allows all traffic
func TestEgressFirewallAllowAll(t *testing.T) {
	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Allow all IPs using 0.0.0.0/0
	allowAll := []string{"0.0.0.0/0"}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowAll,
			DenyOut:  &[]string{sandbox_network.AllInternetTrafficCIDR},
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	assertHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.8.8", true, "Expected curl to 8.8.8.8 to succeed with 0.0.0.0/0 allow")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://1.1.1.1", true, "Expected curl to 1.1.1.1 to succeed with 0.0.0.0/0 allow")
}

// TestEgressFirewallAllowOverridesBlock tests that allowOut takes precedence over blockOut
func TestEgressFirewallAllowOverridesBlock(t *testing.T) {
	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Block specific IP but also allow it - allow should take precedence
	allowedIPs := []string{"1.1.1.1"}
	blockedIPs := []string{"1.1.1.1"}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowedIPs,
			DenyOut:  &blockedIPs,
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	assertHTTPRequest(t, ctx, sbx, envdClient, "https://1.1.1.1", true, "Expected curl to allowed IP (1.1.1.1) to succeed since allow takes precedence over block")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.8.8", true, "Expected curl to other IP (8.8.8.8) to succeed")
}

// TestEgressFirewallMultipleAllowedIPs tests multiple allowed IPs
func TestEgressFirewallMultipleAllowedIPs(t *testing.T) {
	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Allow multiple specific IPs
	allowedIPs := []string{"8.8.8.8", "8.8.4.4", "1.1.1.1"}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowedIPs,
			DenyOut:  &[]string{sandbox_network.AllInternetTrafficCIDR},
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	assertHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.8.8", true, "Expected curl to allowed IP (8.8.8.8) to succeed")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.4.4", true, "Expected curl to allowed IP (8.8.4.4) to succeed")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://1.1.1.1", true, "Expected curl to allowed IP (1.1.1.1) to succeed")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://9.9.9.9", false, "Expected curl to non-allowed IP (9.9.9.9) to fail")
}

// TestEgressFirewallWithInternetAccessFalse tests that network config takes precedence over allow_internet_access
func TestEgressFirewallWithInternetAccessFalse(t *testing.T) {
	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Set both network config and allow_internet_access=false
	// Network config should take precedence - allowed IPs should still work
	allowedIPs := []string{"8.8.8.8"}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithAllowInternetAccess(false),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowedIPs,
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	assertHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.8.8", true, "Expected curl to succeed for allowed IP even with allow_internet_access=false (network config takes precedence)")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://1.1.1.1", false, "Expected curl to non-allowed IP to fail")
}

// TestEgressFirewallPrivateIPRangesAlwaysBlocked tests that private IP ranges cannot be allowed
// Note: Private IP ranges (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16) are always blocked
// by the orchestrator for security reasons. Attempting to specify them in allowOut should result in
// a sandbox creation failure.
func TestEgressFirewallPrivateIPRangesAlwaysBlocked(t *testing.T) {
	templateID := ensureNetworkTestTemplate(t)
	client := setup.GetAPIClient()
	timeout := int32(60)

	testCases := []struct {
		name      string
		allowedIP string
		testIP    string
		testDesc  string
	}{
		{
			name:      "private_range_10.0.0.0/8",
			allowedIP: "10.0.0.0/8",
			testIP:    "10.0.0.1",
			testDesc:  "10.0.0.0/8 range",
		},
		{
			name:      "private_range_192.168.0.0/16",
			allowedIP: "192.168.0.0/16",
			testIP:    "192.168.0.1",
			testDesc:  "192.168.0.0/16 range",
		},
		{
			name:      "private_range_172.16.0.0/12",
			allowedIP: "172.16.0.0/12",
			testIP:    "172.16.0.1",
			testDesc:  "172.16.0.0/12 range",
		},
		{
			name:      "link_local_169.254.0.0/16",
			allowedIP: "169.254.0.0/16",
			testIP:    "169.254.0.1",
			testDesc:  "169.254.0.0/16 range (link-local)",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			// Try to create a sandbox with a private IP range in allowOut
			allowedIPs := []string{tc.allowedIP}

			sbx := utils.SetupSandboxWithCleanup(t, client,
				utils.WithTemplateID(templateID),
				utils.WithTimeout(timeout),
				utils.WithNetwork(&api.SandboxNetworkConfig{
					AllowOut: &allowedIPs,
				}),
			)

			envdClient := setup.GetEnvdClient(t, ctx)

			assertHTTPRequest(t, ctx, sbx, envdClient, tc.testIP, false, fmt.Sprintf("Expected curl to private IP %s to fail", tc.testIP))
		})
	}
}

// TestEgressFirewallAllowAllDuplicate tests that adding 0.0.0.0/0 twice works correctly
func TestEgressFirewallAllowAllDuplicate(t *testing.T) {
	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Add 0.0.0.0/0 twice in the allowOut list
	allowAll := []string{"0.0.0.0/0", "0.0.0.0/0"}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowAll,
			DenyOut:  &[]string{sandbox_network.AllInternetTrafficCIDR},
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	assertHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.8.8", true, "Expected curl to 8.8.8.8 to succeed with duplicate 0.0.0.0/0 allow")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://1.1.1.1", true, "Expected curl to 1.1.1.1 to succeed with duplicate 0.0.0.0/0 allow")
}

// TestEgressFirewallRegularIPThenAllowAll tests that adding a regular IP and then 0.0.0.0/0 works correctly
func TestEgressFirewallRegularIPThenAllowAll(t *testing.T) {
	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Add a specific IP followed by 0.0.0.0/0
	allowList := []string{"8.8.8.8", "0.0.0.0/0"}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowList,
			DenyOut:  &[]string{sandbox_network.AllInternetTrafficCIDR},
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	assertHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.8.8", true, "Expected curl to 8.8.8.8 to succeed")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://1.1.1.1", true, "Expected curl to 1.1.1.1 to succeed (0.0.0.0/0 allows all)")
}

// TestEgressFirewallAllowDomainThroughBlockedInternet tests that a specific domain can be allowed
// when all internet traffic is blocked via 0.0.0.0/0
func TestEgressFirewallAllowDomainThroughBlockedInternet(t *testing.T) {
	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Block all internet but allow google.com domain
	allowList := []string{"google.com"}
	blockAll := []string{sandbox_network.AllInternetTrafficCIDR}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowList,
			DenyOut:  &blockAll,
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	assertHTTPRequest(t, ctx, sbx, envdClient, "https://google.com", true, "Expected curl to allowed domain (google.com) to succeed")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://cloudflare.com", false, "Expected curl to non-allowed domain (cloudflare.com) to fail")
}

// TestEgressFirewallAllowWildcardDomainThroughBlockedInternet tests that wildcard domain patterns work
func TestEgressFirewallAllowWildcardDomainThroughBlockedInternet(t *testing.T) {
	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Block all internet but allow *.google.com wildcard domain
	allowList := []string{"*.google.com"}
	blockAll := []string{sandbox_network.AllInternetTrafficCIDR}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowList,
			DenyOut:  &blockAll,
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	assertHTTPRequest(t, ctx, sbx, envdClient, "https://www.google.com", true, "Expected curl to subdomain (www.google.com) matching wildcard (*.google.com) to succeed")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://maps.google.com", true, "Expected curl to subdomain (maps.google.com) matching wildcard (*.google.com) to succeed")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://cloudflare.com", false, "Expected curl to non-matching domain (cloudflare.com) to fail")
}

// TestEgressFirewallExactDomainMatchVsSubdomain tests that exact domain match does not include subdomains
func TestEgressFirewallExactDomainMatchVsSubdomain(t *testing.T) {
	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Block all internet but allow only exact "google.com" (not subdomains)
	allowList := []string{"google.com"}
	blockAll := []string{sandbox_network.AllInternetTrafficCIDR}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowList,
			DenyOut:  &blockAll,
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	assertHTTPRequest(t, ctx, sbx, envdClient, "https://google.com", true, "Expected curl to exact domain (google.com) to succeed")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://www.google.com", false, "Expected curl to subdomain (www.google.com) to fail when only exact domain (google.com) is allowed")
}

// TestEgressFirewallAllowAllDomainsWildcard tests that "*" wildcard allows all domains
func TestEgressFirewallAllowAllDomainsWildcard(t *testing.T) {
	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Block all internet but allow all domains with "*" wildcard
	allowList := []string{"*"}
	blockAll := []string{sandbox_network.AllInternetTrafficCIDR}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowList,
			DenyOut:  &blockAll,
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	assertHTTPRequest(t, ctx, sbx, envdClient, "https://google.com", true, "Expected curl to google.com to succeed with '*' wildcard")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://cloudflare.com", true, "Expected curl to cloudflare.com to succeed with '*' wildcard")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://github.com", true, "Expected curl to github.com to succeed with '*' wildcard")
}

// TestEgressFirewallDomainCaseInsensitive tests that domain matching is case-insensitive
func TestEgressFirewallDomainCaseInsensitive(t *testing.T) {
	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Block all internet but allow domain with mixed case
	allowList := []string{"Google.COM"}
	blockAll := []string{sandbox_network.AllInternetTrafficCIDR}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowList,
			DenyOut:  &blockAll,
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	assertHTTPRequest(t, ctx, sbx, envdClient, "https://google.com", true, "Expected curl to google.com to succeed when Google.COM is allowed (case-insensitive)")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://cloudflare.com", false, "Expected curl to non-allowed domain (cloudflare.com) to fail")
}

// TestEgressFirewallAllowDomainAndIP tests mixed domain and IP allowlist
func TestEgressFirewallAllowDomainAndIP(t *testing.T) {
	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Block all internet but allow both a domain and an IP
	allowList := []string{"google.com", "1.1.1.1"}
	blockAll := []string{sandbox_network.AllInternetTrafficCIDR}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowList,
			DenyOut:  &blockAll,
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	assertHTTPRequest(t, ctx, sbx, envdClient, "https://google.com", true, "Expected curl to allowed domain (google.com) to succeed")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://1.1.1.1", true, "Expected curl to allowed IP (1.1.1.1) to succeed")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://cloudflare.com", false, "Expected curl to non-allowed domain (cloudflare.com) to fail")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://8.8.4.4", false, "Expected curl to non-allowed IP (8.8.4.4) to fail")
}

// TestEgressFirewallHTTPSByIPNoHostname tests that HTTPS requests by IP (no SNI hostname)
// fall back to CIDR rules when domain filtering is configured
func TestEgressFirewallHTTPSByIPNoHostname(t *testing.T) {
	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Block all internet but allow only a domain (not an IP)
	allowList := []string{"cloudflare.com"}
	blockAll := []string{sandbox_network.AllInternetTrafficCIDR}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowList,
			DenyOut:  &blockAll,
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	assertHTTPRequest(t, ctx, sbx, envdClient, "https://cloudflare.com", true, "Expected curl to allowed domain (cloudflare.com) to succeed")
	// HTTPS request by IP (1.1.1.1 is Cloudflare's IP) is blocked because there's no hostname/SNI to match
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://1.1.1.1", false, "Expected curl to IP (https://1.1.1.1) to fail when only domain is allowed (no SNI hostname)")
}

// TestEgressFirewallDomainPersistsAfterResume tests that domain-based network config persists after pause/resume
func TestEgressFirewallDomainPersistsAfterResume(t *testing.T) {
	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	sbxTimeout := int32(60)
	client := setup.GetAPIClient()

	// Block all internet but allow specific domain
	allowList := []string{"google.com"}
	blockAll := []string{sandbox_network.AllInternetTrafficCIDR}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowList,
			DenyOut:  &blockAll,
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	// Test before pause
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://google.com", true, "Expected curl to allowed domain (google.com) to succeed before pause")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://cloudflare.com", false, "Expected curl to non-allowed domain (cloudflare.com) to fail before pause")

	// Pause the sandbox
	respPause, err := client.PostSandboxesSandboxIDPauseWithResponse(ctx, sbx.SandboxID, setup.WithAPIKey())
	require.NoError(t, err, "Expected to pause sandbox without error")
	require.Equal(t, http.StatusNoContent, respPause.StatusCode(), "Expected status code 204 No Content, got %d", respPause.StatusCode())

	// Resume the sandbox
	respResume, err := client.PostSandboxesSandboxIDResumeWithResponse(ctx, sbx.SandboxID, api.PostSandboxesSandboxIDResumeJSONRequestBody{
		Timeout: &sbxTimeout,
	}, setup.WithAPIKey())
	require.NoError(t, err, "Expected to resume sandbox without error")
	require.Equal(t, http.StatusCreated, respResume.StatusCode(), "Expected status code 201 Created, got %d", respResume.StatusCode())

	// Test after resume
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://google.com", true, "Expected curl to allowed domain (google.com) to succeed after resume")
	assertHTTPRequest(t, ctx, sbx, envdClient, "https://cloudflare.com", false, "Expected curl to non-allowed domain (cloudflare.com) to fail after resume")
}

// =============================================================================
// HTTP (non-HTTPS) protocol tests
// =============================================================================

// TestEgressFirewallHTTPDomainFiltering tests that HTTP (non-HTTPS) traffic is filtered by Host header
func TestEgressFirewallHTTPDomainFiltering(t *testing.T) {
	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Block all internet but allow postman-echo.com domain (supports HTTP)
	allowList := []string{"postman-echo.com"}
	blockAll := []string{sandbox_network.AllInternetTrafficCIDR}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowList,
			DenyOut:  &blockAll,
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	assertHTTPRequest(t, ctx, sbx, envdClient, "http://postman-echo.com/get", true, "Expected HTTP curl to allowed domain (httpbin.org) to succeed")
	assertHTTPRequest(t, ctx, sbx, envdClient, "http://example.com", false, "Expected HTTP curl to non-allowed domain (example.com) to fail")
}

// =============================================================================
// UDP protocol tests (DNS queries to specific servers)
// =============================================================================

// TestEgressFirewallUDPAllowedIP tests that UDP traffic (DNS) to allowed IPs works
func TestEgressFirewallUDPAllowedIP(t *testing.T) {
	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Block all internet but allow Google DNS IP
	allowList := []string{"8.8.8.8"}
	blockAll := []string{sandbox_network.AllInternetTrafficCIDR}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowList,
			DenyOut:  &blockAll,
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	assertDNSQuery(t, ctx, sbx, envdClient, "8.8.8.8", "google.com", true, "Expected DNS query (UDP) to allowed IP (8.8.8.8) to succeed")
	assertDNSQuery(t, ctx, sbx, envdClient, "1.1.1.1", "google.com", false, "Expected DNS query (UDP) to non-allowed IP (1.1.1.1) to fail")
}

// TestEgressFirewallUDPAllowedCIDR tests that UDP traffic to allowed CIDR range works
func TestEgressFirewallUDPAllowedCIDR(t *testing.T) {
	templateID := ensureNetworkTestTemplate(t)
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Block all internet but allow Google DNS CIDR range
	allowList := []string{"8.8.8.0/24"}
	blockAll := []string{sandbox_network.AllInternetTrafficCIDR}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTemplateID(templateID),
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowList,
			DenyOut:  &blockAll,
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	assertDNSQuery(t, ctx, sbx, envdClient, "8.8.8.8", "google.com", true, "Expected DNS query (UDP) to IP within allowed CIDR (8.8.8.0/24) to succeed")
	assertDNSQuery(t, ctx, sbx, envdClient, "1.1.1.1", "google.com", false, "Expected DNS query (UDP) to IP outside allowed CIDR to fail")
}
