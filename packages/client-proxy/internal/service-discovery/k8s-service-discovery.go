package service_discovery

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const (
	k8sQueryRefreshInterval = 10 * time.Second
)

type K8sServiceDiscovery struct {
	logger  *zap.Logger
	entries *smap.Map[ServiceDiscoveryItem]
	client  *kubernetes.Clientset

	filterLabels    string
	filterNamespace string

	hostIP bool
	port   int
}

func NewK8sServiceDiscovery(ctx context.Context, logger *zap.Logger, client *kubernetes.Clientset, port int, podLabels string, podNamespace string) *K8sServiceDiscovery {
	sd := &K8sServiceDiscovery{
		logger: logger,
		client: client,

		port:   port,
		hostIP: true,

		filterLabels:    podLabels,
		filterNamespace: podNamespace,

		entries: smap.New[ServiceDiscoveryItem](),
	}

	go func() { sd.keepInSync(ctx) }()

	return sd
}

func (sd *K8sServiceDiscovery) ListNodes(_ context.Context) ([]ServiceDiscoveryItem, error) {
	entries := sd.entries.Items()
	items := make([]ServiceDiscoveryItem, 0)

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
			sd.logger.Info("Stopping service discovery keep-in-sync")
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
		sd.logger.Error("Failed to describe pods", zap.Error(err))
	}

	foundPods := make(map[string]string)
	for _, pod := range list.Items {
		ip := pod.Status.PodIP
		if sd.hostIP {
			ip = pod.Status.HostIP
		}

		key := fmt.Sprintf("%s:%d", ip, sd.port)
		item := ServiceDiscoveryItem{
			NodeIP:   ip,
			NodePort: sd.port,
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
