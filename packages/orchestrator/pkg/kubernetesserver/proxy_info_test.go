package kubernetesserver

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	reverseproxy "github.com/e2b-dev/infra/packages/shared/pkg/proxy"
)

func readyProxyResources(cfg Config, sandboxID string) (*corev1.Pod, *corev1.Secret) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName(sandboxID),
			Namespace: cfg.Namespace,
			UID:       types.UID("lifecycle-123"),
			Labels:    sandboxLabels(sandboxID, RuntimeClassCLH),
			Annotations: map[string]string{
				identityAnnotation: "identity-123",
				vcpuAnnotation:     "2",
				ramMiBAnnotation:   "1024",
			},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Resources: corev1.ResourceRequirements{},
		}}},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			PodIP: "10.128.3.9",
			Conditions: []corev1.PodCondition{{
				Type: corev1.PodReady, Status: corev1.ConditionTrue,
			}},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName(sandboxID),
			Namespace: cfg.Namespace,
			Labels:    sandboxLabels(sandboxID, RuntimeClassCLH),
			Annotations: map[string]string{
				identityAnnotation: "identity-123",
			},
		},
		Data: map[string][]byte{
			secretTrafficTokenKey: []byte("traffic-secret"),
			secretMaskHostKey:     []byte("service-${PORT}.internal"),
		},
	}
	return pod, secret
}

func TestProxyDestinationAuthenticatesAndUsesCurrentPodIP(t *testing.T) {
	cfg := testConfig(defaultEnvdPort)
	pod, secret := readyProxyResources(cfg, "sbx-0123456789")
	client := fake.NewSimpleClientset(pod, secret)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.test", nil)
	require.NoError(t, err)

	_, err = proxyDestination(t.Context(), client, cfg, request, "sbx-0123456789", 8080)
	var missing *reverseproxy.MissingTrafficAccessTokenError
	assert.True(t, errors.As(err, &missing))

	request.Header.Set(trafficAccessTokenHeader, "traffic-secret")
	destination, err := proxyDestination(t.Context(), client, cfg, request, "sbx-0123456789", 8080)
	require.NoError(t, err)
	assert.Equal(t, "10.128.3.9:8080", destination.Url.Host)
	assert.Equal(t, "lifecycle-123", destination.ConnectionKey)
	require.NotNil(t, destination.MaskRequestHost)
	assert.Equal(t, "service-8080.internal", *destination.MaskRequestHost)

	request.Header.Del(trafficAccessTokenHeader)
	_, err = proxyDestination(t.Context(), client, cfg, request, "sbx-0123456789", uint64(cfg.EnvdPort))
	require.NoError(t, err, "envd performs its own access-token validation")
}

func TestProxyDestinationRejectsSecretFromDifferentPodLifecycle(t *testing.T) {
	cfg := testConfig(defaultEnvdPort)
	pod, secret := readyProxyResources(cfg, "sbx-0123456789")
	secret.Annotations[identityAnnotation] = "stale-identity"
	client := fake.NewSimpleClientset(pod, secret)
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://example.test", nil)
	require.NoError(t, err)

	_, err = proxyDestination(t.Context(), client, cfg, request, "sbx-0123456789", 8080)
	var notFound *reverseproxy.SandboxNotFoundError
	assert.ErrorAs(t, err, &notFound)
}

func TestCachedProxyResourcesTracksPodUpdatesAndSecretDeletion(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cfg := testConfig(defaultEnvdPort)
	pod, secret := readyProxyResources(cfg, "sbx-0123456789")
	client := fake.NewSimpleClientset(pod, secret)

	resources, err := newCachedProxyResources(ctx, client, cfg)
	require.NoError(t, err)
	cachedPod, err := resources.pod(t.Context(), pod.Name)
	require.NoError(t, err)
	assert.Equal(t, pod.Status.PodIP, cachedPod.Status.PodIP)

	pod.Status.PodIP = "10.128.9.21"
	_, err = client.CoreV1().Pods(cfg.Namespace).Update(t.Context(), pod, metav1.UpdateOptions{})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		updated, getErr := resources.pod(t.Context(), pod.Name)
		return getErr == nil && updated.Status.PodIP == "10.128.9.21"
	}, time.Second, 10*time.Millisecond)

	require.NoError(t, client.CoreV1().Secrets(cfg.Namespace).Delete(t.Context(), secret.Name, metav1.DeleteOptions{}))
	require.Eventually(t, func() bool {
		_, getErr := resources.secret(t.Context(), secret.Name)
		return apierrors.IsNotFound(getErr)
	}, time.Second, 10*time.Millisecond)
}

func TestInfoReportsConfiguredCapacityAndPodAllocation(t *testing.T) {
	cfg := testConfig(defaultEnvdPort)
	cfg.NodeLabels = []string{"indentia-ap", RuntimeClassCLH}
	pod, _ := readyProxyResources(cfg, "sbx-0123456789")
	client := fake.NewSimpleClientset(pod)
	server := NewInfo(client, cfg)

	info, err := server.ServiceInfo(t.Context(), &emptypb.Empty{})
	require.NoError(t, err)
	assert.Equal(t, cfg.CapacityCPU, info.GetMetricCpuCount())
	assert.Equal(t, cfg.CapacityMemoryMiB*1024*1024, info.GetMetricMemoryTotalBytes())
	assert.EqualValues(t, 2, info.GetMetricCpuAllocated())
	assert.EqualValues(t, 1024*1024*1024, info.GetMetricMemoryAllocatedBytes())
	assert.EqualValues(t, 1, info.GetMetricSandboxesRunning())
	assert.ElementsMatch(t, []string{"indentia-ap", "kubernetes", consts.OrchestratorRuntimeOCIKataLabel, RuntimeClassCLH, RuntimeClassQEMU}, info.GetLabels())

	before := info.GetServiceStatusChangedAt().AsTime()
	time.Sleep(time.Millisecond)
	_, err = server.ServiceStatusOverride(t.Context(), &orchestratorinfo.ServiceStatusChangeRequest{ServiceStatus: orchestratorinfo.ServiceInfoStatus_Draining})
	require.NoError(t, err)
	info, err = server.ServiceInfo(t.Context(), &emptypb.Empty{})
	require.NoError(t, err)
	assert.Equal(t, orchestratorinfo.ServiceInfoStatus_Draining, info.GetServiceStatus())
	assert.True(t, info.GetServiceStatusChangedAt().AsTime().After(before))
}
