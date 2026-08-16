package kubernetesserver

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

func TestDeniedAllCIDRsProduceDenyAllEgress(t *testing.T) {
	cfg := &orchestrator.SandboxConfig{Network: &orchestrator.SandboxNetworkConfig{
		Egress: &orchestrator.SandboxNetworkEgressConfig{DeniedCidrs: []string{"0.0.0.0/0", "::/0"}},
	}}
	rules, restricted, err := buildEgressRules(cfg)
	require.NoError(t, err)
	assert.True(t, restricted)
	assert.Empty(t, rules, "an empty egress list with PolicyType=Egress denies all traffic")
}

func TestDeniedCIDRsKeepOtherAddressFamilyOpen(t *testing.T) {
	cfg := &orchestrator.SandboxConfig{Network: &orchestrator.SandboxNetworkConfig{
		Egress: &orchestrator.SandboxNetworkEgressConfig{DeniedCidrs: []string{"10.0.1.9/8"}},
	}}
	rules, restricted, err := buildEgressRules(cfg)
	require.NoError(t, err)
	require.True(t, restricted)
	require.Len(t, rules, 1)
	require.Len(t, rules[0].To, 2)
	assert.Contains(t, rules[0].To[0].IPBlock.Except, "10.0.0.0/8")
	assert.Contains(t, rules[0].To[0].IPBlock.Except, "169.254.0.0/16")
	assert.Equal(t, "::/0", rules[0].To[1].IPBlock.CIDR)
	assert.Contains(t, rules[0].To[1].IPBlock.Except, "fc00::/7")
}

func TestAllowedCIDRsAreCanonicalized(t *testing.T) {
	cfg := &orchestrator.SandboxConfig{Network: &orchestrator.SandboxNetworkConfig{
		Egress: &orchestrator.SandboxNetworkEgressConfig{AllowedCidrs: []string{"8.8.8.9/24"}},
	}}
	rules, restricted, err := buildEgressRules(cfg)
	require.NoError(t, err)
	require.True(t, restricted)
	require.Len(t, rules, 1)
	require.Len(t, rules[0].To, 1)
	assert.Equal(t, "8.8.8.0/24", rules[0].To[0].IPBlock.CIDR)
}

func TestDefaultEgressBlocksPrivateAndLocalRanges(t *testing.T) {
	rules, restricted, err := buildEgressRules(&orchestrator.SandboxConfig{})
	require.NoError(t, err)
	require.True(t, restricted)
	require.Len(t, rules, 1)
	require.Len(t, rules[0].To, 2)

	v4 := rules[0].To[0].IPBlock
	assert.Equal(t, "0.0.0.0/0", v4.CIDR)
	assert.Contains(t, v4.Except, "10.0.0.0/8")
	assert.Contains(t, v4.Except, "169.254.0.0/16")
	assert.Contains(t, v4.Except, "172.16.0.0/12")

	v6 := rules[0].To[1].IPBlock
	assert.Equal(t, "::/0", v6.CIDR)
	assert.Contains(t, v6.Except, "fc00::/7")
	assert.Contains(t, v6.Except, "fe80::/10")
}

func TestAllowedCIDRsCannotOpenProtectedRanges(t *testing.T) {
	cfg := &orchestrator.SandboxConfig{Network: &orchestrator.SandboxNetworkConfig{
		Egress: &orchestrator.SandboxNetworkEgressConfig{AllowedCidrs: []string{"10.128.0.0/14"}},
	}}
	_, _, err := buildEgressRules(cfg)
	require.ErrorContains(t, err, "overlaps protected sandbox CIDR")
}

func TestNestedDeniedCIDRIsCollapsedIntoProtectedBaseline(t *testing.T) {
	cfg := &orchestrator.SandboxConfig{Network: &orchestrator.SandboxNetworkConfig{
		Egress: &orchestrator.SandboxNetworkEgressConfig{DeniedCidrs: []string{"10.128.0.0/14"}},
	}}
	rules, restricted, err := buildEgressRules(cfg)
	require.NoError(t, err)
	require.True(t, restricted)
	require.Len(t, rules, 1)
	require.Len(t, rules[0].To, 2)
	assert.Contains(t, rules[0].To[0].IPBlock.Except, "10.0.0.0/8")
	assert.NotContains(t, rules[0].To[0].IPBlock.Except, "10.128.0.0/14")
}

func TestEgressRejectsUnrepresentableDomainPolicy(t *testing.T) {
	cfg := &orchestrator.SandboxConfig{Network: &orchestrator.SandboxNetworkConfig{
		Egress: &orchestrator.SandboxNetworkEgressConfig{AllowedDomains: []string{"example.com"}},
	}}
	_, _, err := buildEgressRules(cfg)
	require.ErrorContains(t, err, "domain-based egress")
}

func TestInitPayloadUsesPodUIDAsLifecycle(t *testing.T) {
	request := testCreateRequest(RuntimeClassCLH)
	request.Sandbox.EnvVars = map[string]string{"A": "B"}
	raw, err := buildInitPayload(request)
	require.NoError(t, err)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{UID: types.UID("pod-lifecycle-42")}}

	encoded, err := initPayloadForPod(raw, pod)
	require.NoError(t, err)
	var payload envdInitPayload
	require.NoError(t, json.Unmarshal(encoded, &payload))
	assert.Equal(t, "pod-lifecycle-42", payload.LifecycleID)
	assert.Equal(t, "B", payload.EnvVars["A"])
	assert.False(t, payload.Timestamp.IsZero())
}
