package discovery

import (
	"net"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

const (
	testNamespace     = "e2b"
	testLabelSelector = "app.kubernetes.io/name=orchestrator"
)

func newOrchestratorPod(name, hostIP string, ready bool) *corev1.Pod {
	condStatus := corev1.ConditionFalse
	if ready {
		condStatus = corev1.ConditionTrue
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name": "orchestrator",
			},
		},
		Status: corev1.PodStatus{
			Phase:  corev1.PodRunning,
			HostIP: hostIP,
			PodIP:  hostIP,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: condStatus},
			},
		},
	}
}

// TestKubernetesDiscovery_PodsWithSharedPrefix verifies that two pods sharing
// a long common prefix (the typical DaemonSet/Deployment pod-name shape) are
// returned with distinct ShortIDs. Truncating to consts.NodeIDLength would
// collide them into a single discovery key and silently drop one of the
// orchestrators; this test guards against that regression.
func TestKubernetesDiscovery_PodsWithSharedPrefix(t *testing.T) {
	t.Parallel()

	pod1 := newOrchestratorPod("orchestrator-abcde-fghij", "10.0.0.1", true)
	pod2 := newOrchestratorPod("orchestrator-abcde-klmno", "10.0.0.2", true)

	client := fake.NewSimpleClientset(pod1, pod2)
	d := NewKubernetes(client, testNamespace, testLabelSelector)

	nodes, err := d.ListNodes(t.Context())
	require.NoError(t, err)
	require.Len(t, nodes, 2)

	// Both pods share the first 8+ characters; without the fix the truncated
	// IDs would be equal.
	assert.NotEqual(t, nodes[0].ShortID, nodes[1].ShortID,
		"pods with a shared prefix must produce distinct ShortIDs")

	// ShortID must equal the full pod name on the K8s backend.
	byShortID := map[string]Node{}
	for _, n := range nodes {
		byShortID[n.ShortID] = n
	}

	require.Contains(t, byShortID, pod1.Name)
	require.Contains(t, byShortID, pod2.Name)

	port := strconv.Itoa(int(consts.OrchestratorAPIPort))
	assert.Equal(t, "10.0.0.1", byShortID[pod1.Name].IPAddress)
	assert.Equal(t, net.JoinHostPort("10.0.0.1", port), byShortID[pod1.Name].OrchestratorAddress)
	assert.Equal(t, "10.0.0.2", byShortID[pod2.Name].IPAddress)
	assert.Equal(t, net.JoinHostPort("10.0.0.2", port), byShortID[pod2.Name].OrchestratorAddress)
}

// TestKubernetesDiscovery_FiltersNotReady ensures that pods which are not
// Ready=True are excluded, mirroring the Nomad backend's "Status == ready"
// filter.
func TestKubernetesDiscovery_FiltersNotReady(t *testing.T) {
	t.Parallel()

	ready := newOrchestratorPod("orchestrator-aaaaa-bbbbb", "10.0.0.1", true)
	notReady := newOrchestratorPod("orchestrator-aaaaa-ccccc", "10.0.0.2", false)

	client := fake.NewSimpleClientset(ready, notReady)
	d := NewKubernetes(client, testNamespace, testLabelSelector)

	nodes, err := d.ListNodes(t.Context())
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, ready.Name, nodes[0].ShortID)
}

// TestKubernetesDiscovery_FiltersPending ensures pods that are not in the
// Running phase are excluded even if they have no conditions yet.
func TestKubernetesDiscovery_FiltersPending(t *testing.T) {
	t.Parallel()

	pending := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orchestrator-pending",
			Namespace: testNamespace,
			Labels:    map[string]string{"app.kubernetes.io/name": "orchestrator"},
		},
		Status: corev1.PodStatus{
			Phase:  corev1.PodPending,
			HostIP: "10.0.0.3",
		},
	}

	client := fake.NewSimpleClientset(pending)
	d := NewKubernetes(client, testNamespace, testLabelSelector)

	nodes, err := d.ListNodes(t.Context())
	require.NoError(t, err)
	assert.Empty(t, nodes)
}

// TestKubernetesDiscovery_FiltersMissingIP ensures pods without HostIP/PodIP
// are excluded so callers never get an unroutable address.
func TestKubernetesDiscovery_FiltersMissingIP(t *testing.T) {
	t.Parallel()

	noIP := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "orchestrator-no-ip",
			Namespace: testNamespace,
			Labels:    map[string]string{"app.kubernetes.io/name": "orchestrator"},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
		},
	}

	client := fake.NewSimpleClientset(noIP)
	d := NewKubernetes(client, testNamespace, testLabelSelector)

	nodes, err := d.ListNodes(t.Context())
	require.NoError(t, err)
	assert.Empty(t, nodes)
}
