package kube

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
	"github.com/e2b-dev/infra/packages/shared/pkg/servicediscovery"
)

const (
	testNamespace     = "e2b"
	testLabelSelector = "app.kubernetes.io/name=orchestrator"
)

func newOrchestratorPod(name, hostIP string, phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/name": "orchestrator",
			},
		},
		Spec: corev1.PodSpec{NodeName: name + "-node"},
		Status: corev1.PodStatus{
			Phase:  phase,
			HostIP: hostIP,
			PodIP:  hostIP,
		},
	}
}

// podReadinessShapes covers the readiness states a Running pod is observed in.
func podReadinessShapes() map[string]func(*corev1.Pod) {
	terminating := func(p *corev1.Pod) {
		p.DeletionTimestamp = new(metav1.Now())
		p.Finalizers = []string{"e2b.dev/test"}
	}
	ready := func(status corev1.ConditionStatus) func(*corev1.Pod) {
		return func(p *corev1.Pod) {
			p.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: status}}
		}
	}

	return map[string]func(*corev1.Pod){
		"not probed yet":           func(*corev1.Pod) {},
		"reports itself unhealthy": ready(corev1.ConditionFalse),
		// Termination itself leaves Ready true — the kubelet exempts readiness
		// from its graceful-shutdown probe stop. Ready goes false separately,
		// when the control plane declares the node NotReady.
		"terminating":               func(p *corev1.Pod) { terminating(p); ready(corev1.ConditionTrue)(p) },
		"on a node marked NotReady": func(p *corev1.Pod) { terminating(p); ready(corev1.ConditionFalse)(p) },
	}
}

// TestKubernetesDiscovery_PodsWithSharedPrefix verifies that two pods sharing
// a long common prefix (the typical DaemonSet/Deployment pod-name shape) are
// returned with distinct IDs. Truncating to consts.NodeIDLength would
// collide them into a single discovery key and silently drop one of the
// orchestrators; this test guards against that regression.
func TestKubernetesDiscovery_PodsWithSharedPrefix(t *testing.T) {
	t.Parallel()

	pod1 := newOrchestratorPod("orchestrator-abcde-fghij", "10.0.0.1", corev1.PodRunning)
	pod2 := newOrchestratorPod("orchestrator-abcde-klmno", "10.0.0.2", corev1.PodRunning)

	client := fake.NewSimpleClientset(pod1, pod2)
	d := NewPods(client, testNamespace, testLabelSelector)

	nodes, err := d.ListInstances(t.Context())
	require.NoError(t, err)
	require.Len(t, nodes, 2)

	// Both pods share the first 8+ characters; without the fix the truncated
	// IDs would be equal.
	assert.NotEqual(t, nodes[0].WorkloadID, nodes[1].WorkloadID,
		"pods with a shared prefix must produce distinct IDs")

	// ID must equal the full pod name on the K8s backend.
	byID := map[string]servicediscovery.Instance{}
	for _, n := range nodes {
		byID[n.WorkloadID] = n
	}

	require.Contains(t, byID, pod1.Name)
	require.Contains(t, byID, pod2.Name)

	port := strconv.Itoa(int(consts.OrchestratorAPIPort))
	assert.Equal(t, pod1.Spec.NodeName, byID[pod1.Name].NodeID, "the machine facet must survive")
	assert.Equal(t, servicediscovery.BackendKubernetes, byID[pod1.Name].Backend)
	assert.Equal(t, "10.0.0.1", byID[pod1.Name].IPAddress)
	assert.Equal(t, net.JoinHostPort("10.0.0.1", port), byID[pod1.Name].Address())
	assert.Equal(t, "10.0.0.2", byID[pod2.Name].IPAddress)
	assert.Equal(t, net.JoinHostPort("10.0.0.2", port), byID[pod2.Name].Address())
}

// An instance that reports itself unhealthy, or has not been probed yet, is
// exactly what the operations targeting it need to reach. A terminating pod
// most of all: it is drained through, not abandoned.
func TestKubernetesDiscovery_IgnoresTheReadyCondition(t *testing.T) {
	t.Parallel()

	for name, shape := range podReadinessShapes() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			pod := newOrchestratorPod("orchestrator-aaaaa-bbbbb", "10.0.0.1", corev1.PodRunning)
			shape(pod)

			d := NewPods(fake.NewSimpleClientset(pod), testNamespace, testLabelSelector)

			nodes, err := d.ListInstances(t.Context())
			require.NoError(t, err)
			require.Len(t, nodes, 1)
			assert.Equal(t, pod.Name, nodes[0].WorkloadID)
		})
	}
}

// The same rule reaches the consumer that carries its own port: one lister,
// and the K8S-PODS deploy reads it through the other constructor.
func TestKubernetesDiscovery_OnPortIgnoresTheReadyConditionToo(t *testing.T) {
	t.Parallel()

	pod := newOrchestratorPod("orchestrator-aaaaa-bbbbb", "10.0.0.1", corev1.PodRunning)
	podReadinessShapes()["reports itself unhealthy"](pod)

	d := NewPodsOnPort(fake.NewSimpleClientset(pod), testNamespace, testLabelSelector, 6123, true)

	nodes, err := d.ListInstances(t.Context())
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, "10.0.0.1:6123", nodes[0].Address())
}

// A pod that has finished is gone whatever its last IP was.
func TestKubernetesDiscovery_FiltersCompletedPods(t *testing.T) {
	t.Parallel()

	succeeded := newOrchestratorPod("orchestrator-aaaaa-ccccc", "10.0.0.1", corev1.PodSucceeded)
	failed := newOrchestratorPod("orchestrator-aaaaa-ddddd", "10.0.0.2", corev1.PodFailed)

	client := fake.NewSimpleClientset(succeeded, failed)
	d := NewPods(client, testNamespace, testLabelSelector)

	nodes, err := d.ListInstances(t.Context())
	require.NoError(t, err)
	assert.Empty(t, nodes)
}

// TestKubernetesDiscovery_FiltersPending ensures pods that are not in the
// Running phase are excluded. A scheduled host-networked pod already carries
// the node IP while its init containers run, so only the phase can exclude it.
func TestKubernetesDiscovery_FiltersPending(t *testing.T) {
	t.Parallel()

	pending := newOrchestratorPod("orchestrator-pending", "10.0.0.3", corev1.PodPending)

	client := fake.NewSimpleClientset(pending)
	d := NewPods(client, testNamespace, testLabelSelector)

	nodes, err := d.ListInstances(t.Context())
	require.NoError(t, err)
	assert.Empty(t, nodes)
}

// TestKubernetesDiscovery_FiltersMissingIP ensures pods without HostIP/PodIP
// are excluded so callers never get an unroutable address.
func TestKubernetesDiscovery_FiltersMissingIP(t *testing.T) {
	t.Parallel()

	noIP := newOrchestratorPod("orchestrator-no-ip", "", corev1.PodRunning)

	client := fake.NewSimpleClientset(noIP)
	d := NewPods(client, testNamespace, testLabelSelector)

	nodes, err := d.ListInstances(t.Context())
	require.NoError(t, err)
	assert.Empty(t, nodes)
}
