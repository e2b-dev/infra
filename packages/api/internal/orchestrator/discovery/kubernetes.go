package discovery

import (
	"context"
	"fmt"
	"net"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

// k8sDiscovery implements Discovery by listing pods of the orchestrator
// DaemonSet via the K8s API server.
//
// Orchestrator pods run with host_network=true (see iac/k8s/job-orchestrator),
// so each pod's status.HostIP equals status.PodIP and is the address the
// orchestrator gRPC server listens on. We only return pods that are Running and
// have Ready=True, mirroring the Nomad equivalent's "Status == ready" filter.
type k8sDiscovery struct {
	client        kubernetes.Interface
	namespace     string
	labelSelector string
}

// NewKubernetes creates a Kubernetes-backed Discovery.
//
// labelSelector is a metav1 label selector string, e.g. "app.kubernetes.io/name=orchestrator".
func NewKubernetes(client kubernetes.Interface, namespace, labelSelector string) Discovery {
	return &k8sDiscovery{
		client:        client,
		namespace:     namespace,
		labelSelector: labelSelector,
	}
}

func (d *k8sDiscovery) ListNodes(ctx context.Context) ([]Node, error) {
	ctx, span := tracer.Start(ctx, "list-k8s-nodes")
	defer span.End()

	pods, err := d.client.CoreV1().Pods(d.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: d.labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("list orchestrator pods: %w", err)
	}

	out := make([]Node, 0, len(pods.Items))
	for _, p := range pods.Items {
		if !podReady(&p) {
			continue
		}

		// host_network=true means HostIP == PodIP and is reachable from any
		// pod in the cluster (kube-proxy isn't even involved).
		ip := p.Status.HostIP
		if ip == "" {
			ip = p.Status.PodIP
		}
		if ip == "" {
			continue
		}

		// Use the full pod name as the ShortID.
		//
		// Tradeoff: this intentionally diverges from the Nomad backend, which
		// truncates the node ID to consts.NodeIDLength (8 chars). Nomad node
		// IDs are UUIDs, so the first 8 hex chars are effectively unique
		// across a fleet. Kubernetes pod names, by contrast, share a long
		// common prefix from the DaemonSet/Deployment they belong to (e.g.
		// "orchestrator-xxxxx-yyyyy"), so truncating to 8 chars would collide
		// and collapse every orchestrator pod into a single discovery key.
		// That would break Orchestrator.GetNodeByNomadShortID,
		// connectGroup.Do(...) singleflighting, and syncNode's equality match,
		// causing all but one pod to be silently dropped.
		//
		// The width invariant (8 chars) is therefore relaxed only for the
		// K8s backend. The ShortID is opaque to all downstream consumers --
		// they treat it as a string compare key with no assumed width.
		shortID := p.Name

		out = append(out, Node{
			ShortID:             shortID,
			IPAddress:           ip,
			OrchestratorAddress: net.JoinHostPort(ip, strconv.Itoa(int(consts.OrchestratorAPIPort))),
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
