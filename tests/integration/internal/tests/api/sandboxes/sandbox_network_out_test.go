package sandboxes

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

const internetBlockAddress = "0.0.0.0/0"

// TestEgressFirewallAllowSpecificIP tests that only allowed IPs can be accessed
func TestEgressFirewallAllowSpecificIP(t *testing.T) {
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Use Google's DNS IP (8.8.8.8) as allowed, and Cloudflare's (1.1.1.1) as implicitly blocked
	allowedIPs := []string{"8.8.8.8"}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowedIPs,
			DenyOut:  &[]string{internetBlockAddress},
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	// Test that allowed IP is accessible
	err := utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://8.8.8.8")
	require.NoError(t, err, "Expected curl to allowed IP 8.8.8.8 to succeed")

	// Test that non-allowed IP is blocked
	err = utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://1.1.1.1")
	require.Error(t, err, "Expected curl to non-allowed IP 1.1.1.1 to fail")
	require.Contains(t, err.Error(), "failed with exit code", "Expected connection failure message")
}

// TestEgressFirewallBlockSpecificIP tests that blocked IPs cannot be accessed
func TestEgressFirewallBlockSpecificIP(t *testing.T) {
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Block Cloudflare's DNS IP
	blockedIPs := []string{"1.1.1.1"}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			DenyOut: &blockedIPs,
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	// Test that blocked IP is not accessible
	err := utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://1.1.1.1")
	require.Error(t, err, "Expected curl to blocked IP 1.1.1.1 to fail")
	require.Contains(t, err.Error(), "failed with exit code", "Expected connection failure message")

	// Test that non-blocked IP is accessible
	err = utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://8.8.8.8")
	require.NoError(t, err, "Expected curl to non-blocked IP 8.8.8.8 to succeed")
}

// TestEgressFirewallAllowCIDRRange tests that CIDR ranges work for allowing IPs
func TestEgressFirewallAllowCIDRRange(t *testing.T) {
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Allow Cloudflare's range (1.1.1.0/24) which includes 1.1.1.1
	allowedRanges := []string{"1.1.1.0/24"}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowedRanges,
			DenyOut:  &[]string{internetBlockAddress},
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	// Test that IP within allowed CIDR range is accessible
	err := utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://1.1.1.1")
	require.NoError(t, err, "Expected curl to IP within allowed CIDR range (1.1.1.1) to succeed")

	// Test that IP outside allowed CIDR range is blocked (8.8.8.8 is not in 1.1.1.0/24)
	err = utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://8.8.8.8")
	require.Error(t, err, "Expected curl to IP outside allowed CIDR range (8.8.8.8) to fail")
	require.Contains(t, err.Error(), "failed with exit code", "Expected connection failure message")
}

// TestEgressFirewallBlockCIDRRange tests that CIDR ranges work for blocking IPs
func TestEgressFirewallBlockCIDRRange(t *testing.T) {
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Block Cloudflare's DNS range (1.1.1.0/24)
	blockedRanges := []string{"1.1.1.0/24"}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			DenyOut: &blockedRanges,
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	// Test that IP within blocked CIDR range is not accessible
	err := utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://1.1.1.1")
	require.Error(t, err, "Expected curl to IP within blocked CIDR range (1.1.1.1) to fail")
	require.Contains(t, err.Error(), "failed with exit code", "Expected connection failure message")

	// Test another IP in the same blocked range
	err = utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://1.1.1.2")
	require.Error(t, err, "Expected curl to IP within blocked CIDR range (1.1.1.2) to fail")
	require.Contains(t, err.Error(), "failed with exit code", "Expected connection failure message")

	// Test that IP outside blocked CIDR range is accessible
	err = utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://8.8.8.8")
	require.NoError(t, err, "Expected curl to IP outside blocked CIDR range (8.8.8.8) to succeed")
}

// TestEgressFirewallAllowAndBlockCombination tests that allowOut takes precedence over blockOut
func TestEgressFirewallAllowAndBlockCombination(t *testing.T) {
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Block all internet but explicitly allow one IP - allowOut should take precedence
	allowedIPs := []string{"8.8.8.8"}
	blockAll := []string{internetBlockAddress}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowedIPs,
			DenyOut:  &blockAll,
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	// Test that explicitly allowed IP is accessible even though 0.0.0.0/0 is blocked
	err := utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://8.8.8.8")
	require.NoError(t, err, "Expected curl to explicitly allowed IP (8.8.8.8) to succeed even though 0.0.0.0/0 is blocked")

	// Test that other IPs are still blocked
	err = utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://1.1.1.1")
	require.Error(t, err, "Expected curl to non-allowed IP (1.1.1.1) to fail")
	require.Contains(t, err.Error(), "failed with exit code", "Expected connection failure message")
}

// TestEgressFirewallPersistsAfterResume tests that network config persists after pause/resume
func TestEgressFirewallPersistsAfterResume(t *testing.T) {
	ctx := t.Context()
	sbxTimeout := int32(60)
	client := setup.GetAPIClient()

	// Allow only specific IPs
	allowedIPs := []string{"8.8.8.8", "8.8.4.4"}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowedIPs,
			DenyOut:  &[]string{internetBlockAddress},
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	// Test that allowed IP is accessible before pause
	err := utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://8.8.8.8")
	require.NoError(t, err, "Expected curl to allowed IP (8.8.8.8) to succeed before pause")

	// Test that non-allowed IP is blocked before pause
	err = utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://1.1.1.1")
	require.Error(t, err, "Expected curl to non-allowed IP (1.1.1.1) to fail before pause")

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

	// Test that network config persists - allowed IP is still accessible
	err = utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://8.8.8.8")
	require.NoError(t, err, "Expected curl to allowed IP (8.8.8.8) to succeed after resume")

	// Test that network config persists - non-allowed IP is still blocked
	err = utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://1.1.1.1")
	require.Error(t, err, "Expected curl to non-allowed IP (1.1.1.1) to fail after resume")
	require.Contains(t, err.Error(), "failed with exit code", "Expected connection failure message")
}

// TestEgressFirewallEmptyConfig tests that empty allowOut list is treated as no restriction
func TestEgressFirewallEmptyConfig(t *testing.T) {
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Create sandbox with empty allowOut list - should be treated as no restriction
	emptyAllowList := []string{}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &emptyAllowList,
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	// Test that with empty allowOut list, IPs are accessible (no restriction applied)
	err := utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://8.8.8.8")
	require.NoError(t, err, "Expected curl to succeed with empty allowOut list")
}

// TestEgressFirewallAllowAll tests that 0.0.0.0/0 allows all traffic
func TestEgressFirewallAllowAll(t *testing.T) {
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Allow all IPs using 0.0.0.0/0
	allowAll := []string{"0.0.0.0/0"}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowAll,
			DenyOut:  &[]string{internetBlockAddress},
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	// Test that various IPs are accessible
	err := utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://8.8.8.8")
	require.NoError(t, err, "Expected curl to 8.8.8.8 to succeed with 0.0.0.0/0 allow")

	err = utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://1.1.1.1")
	require.NoError(t, err, "Expected curl to 1.1.1.1 to succeed with 0.0.0.0/0 allow")
}

// TestEgressFirewallAllowOverridesBlock tests that allowOut takes precedence over blockOut
func TestEgressFirewallAllowOverridesBlock(t *testing.T) {
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Block specific IP but also allow it - allow should take precedence
	allowedIPs := []string{"1.1.1.1"}
	blockedIPs := []string{"1.1.1.1"}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowedIPs,
			DenyOut:  &blockedIPs,
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	// Test that allowed IP is accessible even though it's also in blockOut (allow takes precedence)
	err := utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://1.1.1.1")
	require.NoError(t, err, "Expected curl to allowed IP (1.1.1.1) to succeed since allow takes precedence over block")

	// Test that other IPs are accessible (internet is open by default)
	err = utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://8.8.8.8")
	require.NoError(t, err, "Expected curl to other IP (8.8.8.8) to succeed")
}

// TestEgressFirewallMultipleAllowedIPs tests multiple allowed IPs
func TestEgressFirewallMultipleAllowedIPs(t *testing.T) {
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Allow multiple specific IPs
	allowedIPs := []string{"8.8.8.8", "8.8.4.4", "1.1.1.1"}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowedIPs,
			DenyOut:  &[]string{internetBlockAddress},
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	// Test that all allowed IPs are accessible
	err := utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://8.8.8.8")
	require.NoError(t, err, "Expected curl to allowed IP (8.8.8.8) to succeed")

	err = utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://8.8.4.4")
	require.NoError(t, err, "Expected curl to allowed IP (8.8.4.4) to succeed")

	err = utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://1.1.1.1")
	require.NoError(t, err, "Expected curl to allowed IP (1.1.1.1) to succeed")

	// Test that non-allowed IP is blocked
	err = utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://9.9.9.9")
	require.Error(t, err, "Expected curl to non-allowed IP (9.9.9.9) to fail")
	require.Contains(t, err.Error(), "failed with exit code", "Expected connection failure message")
}

// TestEgressFirewallWithInternetAccessFalse tests that network config takes precedence over allow_internet_access
func TestEgressFirewallWithInternetAccessFalse(t *testing.T) {
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Set both network config and allow_internet_access=false
	// Network config should take precedence - allowed IPs should still work
	allowedIPs := []string{"8.8.8.8"}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTimeout(60),
		utils.WithAllowInternetAccess(false),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowedIPs,
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	// Network config takes precedence - allowed IP should be accessible
	err := utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://8.8.8.8")
	require.NoError(t, err, "Expected curl to succeed for allowed IP even with allow_internet_access=false (network config takes precedence)")

	// Non-allowed IPs should still be blocked
	err = utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://1.1.1.1")
	require.Error(t, err, "Expected curl to non-allowed IP to fail")
	require.Contains(t, err.Error(), "failed with exit code", "Expected connection failure message")
}

// TestEgressFirewallPrivateIPRangesAlwaysBlocked tests that private IP ranges cannot be allowed
// Note: Private IP ranges (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, 169.254.0.0/16) are always blocked
// by the orchestrator for security reasons. Attempting to specify them in allowOut should result in
// a sandbox creation failure.
func TestEgressFirewallPrivateIPRangesAlwaysBlocked(t *testing.T) {
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
			// Try to create a sandbox with a private IP range in allowOut
			allowedIPs := []string{tc.allowedIP}

			sbx := utils.SetupSandboxWithCleanup(t, client,
				utils.WithTimeout(timeout),
				utils.WithNetwork(&api.SandboxNetworkConfig{
					AllowOut: &allowedIPs,
				}),
			)

			envdClient := setup.GetEnvdClient(t, t.Context())

			// Non-allowed IPs should still be blocked
			err := utils.ExecCommand(t, t.Context(), sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", tc.testIP)
			require.Error(t, err, "Expected curl to non-allowed IP %s to fail", tc.testIP)
			require.Contains(t, err.Error(), "failed with exit code", "Expected connection failure message")
		})
	}
}

// TestEgressFirewallAllowAllDuplicate tests that adding 0.0.0.0/0 twice works correctly
func TestEgressFirewallAllowAllDuplicate(t *testing.T) {
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Add 0.0.0.0/0 twice in the allowOut list
	allowAll := []string{"0.0.0.0/0", "0.0.0.0/0"}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowAll,
			DenyOut:  &[]string{internetBlockAddress},
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	// Test that various IPs are accessible (duplicate 0.0.0.0/0 should work like a single one)
	err := utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://8.8.8.8")
	require.NoError(t, err, "Expected curl to 8.8.8.8 to succeed with duplicate 0.0.0.0/0 allow")

	err = utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://1.1.1.1")
	require.NoError(t, err, "Expected curl to 1.1.1.1 to succeed with duplicate 0.0.0.0/0 allow")
}

// TestEgressFirewallRegularIPThenAllowAll tests that adding a regular IP and then 0.0.0.0/0 works correctly
func TestEgressFirewallRegularIPThenAllowAll(t *testing.T) {
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Add a specific IP followed by 0.0.0.0/0
	allowList := []string{"8.8.8.8", "0.0.0.0/0"}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowList,
			DenyOut:  &[]string{internetBlockAddress},
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	// Test that the specific IP is accessible
	err := utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://8.8.8.8")
	require.NoError(t, err, "Expected curl to 8.8.8.8 to succeed")

	// Test that other IPs are also accessible (0.0.0.0/0 allows everything)
	err = utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://1.1.1.1")
	require.NoError(t, err, "Expected curl to 1.1.1.1 to succeed (0.0.0.0/0 allows all)")
}

// TestEgressFirewallAllowAllThenRegularIP tests that adding 0.0.0.0/0 and then a regular IP works correctly
func TestEgressFirewallAllowAllThenRegularIP(t *testing.T) {
	ctx := t.Context()
	client := setup.GetAPIClient()

	// Add 0.0.0.0/0 followed by a specific IP
	allowList := []string{"0.0.0.0/0", "8.8.8.8"}

	sbx := utils.SetupSandboxWithCleanup(t, client,
		utils.WithTimeout(60),
		utils.WithNetwork(&api.SandboxNetworkConfig{
			AllowOut: &allowList,
			DenyOut:  &[]string{internetBlockAddress},
		}),
	)

	envdClient := setup.GetEnvdClient(t, ctx)

	// Test that the specific IP is accessible
	err := utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://8.8.8.8")
	require.NoError(t, err, "Expected curl to 8.8.8.8 to succeed")

	// Test that other IPs are also accessible (0.0.0.0/0 allows everything)
	err = utils.ExecCommand(t, ctx, sbx, envdClient, "curl", "--connect-timeout", "3", "--max-time", "5", "-Iks", "https://1.1.1.1")
	require.NoError(t, err, "Expected curl to 1.1.1.1 to succeed (0.0.0.0/0 allows all)")
}
