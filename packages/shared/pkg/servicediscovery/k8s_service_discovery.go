package servicediscovery

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const (
	k8sQueryRefreshInterval = 10 * time.Second
)

// K8sServiceDiscovery caches every pod matching a label selector. Unlike
// k8sDiscovery it applies no phase or readiness filter and publishes a bare
// "<empty>:<port>" entry for a pod that has no IP yet.
type K8sServiceDiscovery struct {
	logger  logger.Logger
	entries *smap.Map[Instance]
	client  kubernetes.Interface

	filterLabels    string
	filterNamespace string

	hostIP bool
	port   uint16

	cancel func()
}

func NewK8sServiceDiscovery(logger logger.Logger, client kubernetes.Interface, port uint16, podLabels string, podNamespace string, hostIP bool) *K8sServiceDiscovery {
	sd := &K8sServiceDiscovery{
		logger: logger,
		client: client,

		port:   port,
		hostIP: hostIP,

		filterLabels:    podLabels,
		filterNamespace: podNamespace,

		entries: smap.New[Instance](),
		cancel:  func() {},
	}

	return sd
}

func (sd *K8sServiceDiscovery) Start(ctx context.Context) {
	ctx, sd.cancel = context.WithCancel(ctx)

	go sd.keepInSync(ctx)
}

func (sd *K8sServiceDiscovery) Stop(_ context.Context) {
	sd.cancel()
}

func (sd *K8sServiceDiscovery) ListInstances(_ context.Context) ([]Instance, error) {
	entries := sd.entries.Items()
	items := make([]Instance, 0)

	for _, item := range entries {
		items = append(items, item)
	}

	return items, nil
}

func (sd *K8sServiceDiscovery) keepInSync(ctx context.Context) {
	// Run the first sync immediately
	sd.sync(ctx)

	ticker := time.NewTicker(k8sQueryRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			sd.logger.Info(ctx, "Stopping service discovery keep-in-sync")

			return
		case <-ticker.C:
			sd.sync(ctx)
		}
	}
}

func (sd *K8sServiceDiscovery) sync(ctx context.Context) {
	reqCtx, reqCancel := context.WithTimeout(ctx, k8sQueryRefreshInterval)
	defer reqCancel()

	list, err := sd.client.CoreV1().Pods(sd.filterNamespace).List(reqCtx, metav1.ListOptions{LabelSelector: sd.filterLabels})
	if err != nil {
		sd.logger.Error(ctx, "Failed to describe pods", zap.Error(err))

		return
	}

	foundPods := make(map[string]string)
	for _, pod := range list.Items {
		ip := pod.Status.PodIP

		// Allow to optionally switch and use HostIP as service discovery entry
		if sd.hostIP {
			ip = pod.Status.HostIP
		}

		key := fmt.Sprintf("%s:%d", ip, sd.port)
		item := Instance{
			WorkloadID: key,
			IPAddress:  ip,
			Port:       sd.port,
		}

		sd.entries.Insert(key, item)
		foundPods[key] = key
	}

	// Remove entries that are no longer in Kubernetes API response
	for key := range sd.entries.Items() {
		if _, ok := foundPods[key]; !ok {
			sd.entries.Remove(key)
		}
	}
}
