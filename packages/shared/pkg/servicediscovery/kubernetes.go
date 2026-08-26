package servicediscovery

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

// k8sDiscovery lists pods of the orchestrator DaemonSet via the K8s API
// server.
//
// Those pods run with host networking, so each pod's status.HostIP equals
// status.PodIP and is the address the orchestrator gRPC server listens on. We
// only return pods that are Running and have Ready=True, mirroring the Nomad
// equivalent's "Status == ready" filter.
type k8sDiscovery struct {
	noSync

	client        kubernetes.Interface
	namespace     string
	labelSelector string
}

// NewKubernetes creates a Kubernetes-backed Discoverer.
//
// labelSelector is a metav1 label selector string, e.g. "app.kubernetes.io/name=orchestrator".
func NewKubernetes(client kubernetes.Interface, namespace, labelSelector string) Discoverer {
	return &k8sDiscovery{
		client:        client,
		namespace:     namespace,
		labelSelector: labelSelector,
	}
}

func (d *k8sDiscovery) ListInstances(ctx context.Context) ([]Instance, error) {
	ctx, span := tracer.Start(ctx, "list-k8s-nodes")
	defer span.End()

	pods, err := d.client.CoreV1().Pods(d.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: d.labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("list orchestrator pods: %w", err)
	}

	out := make([]Instance, 0, len(pods.Items))
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

		// The full pod name, deliberately untruncated: pods of one
		// DaemonSet/Deployment share a long prefix ("orchestrator-xxxxx-yyyyy"),
		// so cutting to consts.NodeIDLength would collapse every orchestrator
		// into a single discovery key and silently drop all but one. The
		// Nomad backends' 8-char width is therefore not an invariant of
		// Instance.ID.
		out = append(out, Instance{
			ID:        p.Name,
			IPAddress: ip,
			Port:      consts.OrchestratorAPIPort,
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
