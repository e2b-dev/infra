package servicediscovery

import (
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const cachedPort = 5008

func newCachedPod(name, podIP, hostIP string, phase corev1.PodPhase) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
			Labels:    map[string]string{"app.kubernetes.io/name": "orchestrator"},
		},
		Status: corev1.PodStatus{Phase: phase, PodIP: podIP, HostIP: hostIP},
	}
}

func listedIPs(t *testing.T, sd *K8sServiceDiscovery) []string {
	t.Helper()

	items, err := sd.ListInstances(t.Context())
	require.NoError(t, err)

	ips := make([]string, 0, len(items))
	for _, i := range items {
		ips = append(ips, i.IPAddress)
	}
	slices.Sort(ips)

	return ips
}

func TestK8sServiceDiscovery_PublishesEveryMatchingPodIncludingPendingAndCompleted(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset(
		newCachedPod("orchestrator-running", "10.0.0.1", "10.1.0.1", corev1.PodRunning),
		newCachedPod("orchestrator-pending", "", "", corev1.PodPending),
		newCachedPod("orchestrator-succeeded", "10.0.0.3", "10.1.0.3", corev1.PodSucceeded),
	)

	sd := NewK8sServiceDiscovery(logger.NewNopLogger(), client, cachedPort, testLabelSelector, testNamespace, false)
	sd.sync(t.Context())

	assert.Equal(t, []string{"", "10.0.0.1", "10.0.0.3"}, listedIPs(t, sd))
}

func TestK8sServiceDiscovery_HostIPFlagSelectsTheAddressSource(t *testing.T) {
	t.Parallel()

	pod := newCachedPod("orchestrator-running", "10.0.0.1", "10.1.0.1", corev1.PodRunning)

	podIPSD := NewK8sServiceDiscovery(logger.NewNopLogger(), fake.NewSimpleClientset(pod), cachedPort, testLabelSelector, testNamespace, false)
	podIPSD.sync(t.Context())
	assert.Equal(t, []string{"10.0.0.1"}, listedIPs(t, podIPSD))

	hostIPSD := NewK8sServiceDiscovery(logger.NewNopLogger(), fake.NewSimpleClientset(pod), cachedPort, testLabelSelector, testNamespace, true)
	hostIPSD.sync(t.Context())
	assert.Equal(t, []string{"10.1.0.1"}, listedIPs(t, hostIPSD))
}

func TestK8sServiceDiscovery_DropsPodsMissingFromTheNextSync(t *testing.T) {
	t.Parallel()

	stays := newCachedPod("orchestrator-stays", "10.0.0.1", "10.1.0.1", corev1.PodRunning)
	goes := newCachedPod("orchestrator-goes", "10.0.0.2", "10.1.0.2", corev1.PodRunning)

	client := fake.NewSimpleClientset(stays, goes)
	sd := NewK8sServiceDiscovery(logger.NewNopLogger(), client, cachedPort, testLabelSelector, testNamespace, false)
	sd.sync(t.Context())
	require.Equal(t, []string{"10.0.0.1", "10.0.0.2"}, listedIPs(t, sd))

	require.NoError(t, client.CoreV1().Pods(testNamespace).Delete(t.Context(), goes.Name, metav1.DeleteOptions{}))
	sd.sync(t.Context())

	assert.Equal(t, []string{"10.0.0.1"}, listedIPs(t, sd))
}

// A dead source reads as an indefinitely stale one: the failed sync keeps the
// previous entries and ListInstances still reports success. This is the half
// of the divergence the query adapters do not have — they return the error and
// the caller skips the cycle.
func TestK8sServiceDiscovery_FailedSyncKeepsTheLastKnownSetAndReportsNoError(t *testing.T) {
	t.Parallel()

	client := fake.NewSimpleClientset(newCachedPod("orchestrator-running", "10.0.0.1", "10.1.0.1", corev1.PodRunning))
	sd := NewK8sServiceDiscovery(logger.NewNopLogger(), client, cachedPort, testLabelSelector, testNamespace, false)
	sd.sync(t.Context())
	require.Equal(t, []string{"10.0.0.1"}, listedIPs(t, sd))

	client.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("api server unreachable")
	})
	sd.sync(t.Context())

	assert.Equal(t, []string{"10.0.0.1"}, listedIPs(t, sd))
}
