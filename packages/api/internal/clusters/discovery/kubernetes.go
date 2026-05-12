package discovery

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// KubernetesServiceDiscovery enumerates template-manager pods via the K8s API,
// the equivalent of LocalServiceDiscovery's Nomad allocation listing.
//
// In the K8s deploy template-manager runs as a Deployment with host_network=true,
// so each pod's status.HostIP is the address its gRPC server listens on.
type KubernetesServiceDiscovery struct {
	client        kubernetes.Interface
	clusterID     uuid.UUID
	namespace     string
	labelSelector string
}

// NewKubernetesDiscovery wires a Kubernetes-backed Discovery for the template
// builder pool inside the (logical) local cluster.
func NewKubernetesDiscovery(clusterID uuid.UUID, client kubernetes.Interface, namespace, labelSelector string) Discovery {
	return &KubernetesServiceDiscovery{
		client:        client,
		clusterID:     clusterID,
		namespace:     namespace,
		labelSelector: labelSelector,
	}
}

func (sd *KubernetesServiceDiscovery) Query(ctx context.Context) ([]Item, error) {
	ctx, span := tracer.Start(ctx, "query-k8s-cluster-nodes", trace.WithAttributes(telemetry.WithClusterID(sd.clusterID)))
	defer span.End()

	pods, err := sd.client.CoreV1().Pods(sd.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: sd.labelSelector,
	})
	if err != nil {
		span.RecordError(err)

		return nil, fmt.Errorf("list template-manager pods: %w", err)
	}

	out := make([]Item, 0, len(pods.Items))
	for _, p := range pods.Items {
		if !podReady(&p) {
			continue
		}

		ip := p.Status.HostIP
		if ip == "" {
			ip = p.Status.PodIP
		}
		if ip == "" {
			continue
		}

		out = append(out, Item{
			// Pod UID is unique and stable across the pod's lifetime.
			UniqueIdentifier: string(p.UID),
			NodeID:           p.Spec.NodeName,
			// InstanceID is "unknown" in the local-cluster path -- mirrors
			// LocalServiceDiscovery's Nomad-based output.
			InstanceID: "unknown",

			LocalIPAddress:       ip,
			LocalInstanceApiPort: consts.OrchestratorAPIPort,
		})
	}

	return out, nil
}

func podReady(p *corev1.Pod) bool {
	if p.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			return true
		}
	}

	return false
}
