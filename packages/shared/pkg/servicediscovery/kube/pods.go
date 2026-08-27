package kube

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/servicediscovery"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/shared/pkg/servicediscovery/kube")

// k8sDiscovery lists pods of the orchestrator DaemonSet via the K8s API
// server.
//
// Those pods run with host networking, so each pod's status.HostIP equals
// status.PodIP and is the address the orchestrator gRPC server listens on. We
// only return pods that are Running and have Ready=True, mirroring the Nomad
// equivalent's "Status == ready" filter.
type k8sDiscovery struct {
	servicediscovery.NoSync

	client        kubernetes.Interface
	namespace     string
	labelSelector string

	// port and preferPodIP exist because the two callers address these pods
	// differently: the orchestrator plane dials the well-known port on the host
	// network, while a configured consumer may carry its own port and read the
	// pod IP. One lister, two wirings.
	port        uint16
	preferPodIP bool
}

// NewPods creates a Kubernetes-backed servicediscovery.Discoverer.
//
// labelSelector is a metav1 label selector string, e.g. "app.kubernetes.io/name=orchestrator".
func NewPods(client kubernetes.Interface, namespace, labelSelector string) servicediscovery.Discoverer {
	return &k8sDiscovery{
		client:        client,
		namespace:     namespace,
		labelSelector: labelSelector,
		port:          consts.OrchestratorAPIPort,
	}
}

// NewPodsOnPort is NewPods for a consumer that carries its own port
// and, unless preferHostIP, addresses pods by their pod IP.
func NewPodsOnPort(client kubernetes.Interface, namespace, labelSelector string, port uint16, preferHostIP bool) servicediscovery.Discoverer {
	return &k8sDiscovery{
		client:        client,
		namespace:     namespace,
		labelSelector: labelSelector,
		port:          port,
		preferPodIP:   !preferHostIP,
	}
}

func (d *k8sDiscovery) ListInstances(ctx context.Context) ([]servicediscovery.Instance, error) {
	ctx, span := tracer.Start(ctx, "list-k8s-nodes")
	defer span.End()

	pods, err := d.client.CoreV1().Pods(d.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: d.labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("list orchestrator pods: %w", err)
	}

	out := make([]servicediscovery.Instance, 0, len(pods.Items))
	for _, p := range pods.Items {
		if !podReady(&p) {
			continue
		}

		ip := d.addressOf(p)
		if ip == "" {
			continue
		}

		// The full pod name, deliberately untruncated: pods of one
		// DaemonSet/Deployment share a long prefix ("orchestrator-xxxxx-yyyyy"),
		// so cutting to consts.NodeIDLength would collapse every orchestrator
		// into a single discovery key and silently drop all but one. The
		// Nomad backends' 8-char width is therefore not an invariant of
		// servicediscovery.Instance.ID.
		out = append(out, servicediscovery.Instance{
			WorkloadID: p.Name,
			NodeID:     p.Spec.NodeName,
			IPAddress:  ip,
			Port:       d.port,
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

func (d *k8sDiscovery) addressOf(p corev1.Pod) string {
	if d.preferPodIP {
		return p.Status.PodIP
	}

	if p.Status.HostIP != "" {
		return p.Status.HostIP
	}

	return p.Status.PodIP
}
