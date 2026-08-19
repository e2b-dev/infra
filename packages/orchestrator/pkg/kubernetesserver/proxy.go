package kubernetesserver

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	reverseproxy "github.com/e2b-dev/infra/packages/shared/pkg/proxy"
	"github.com/e2b-dev/infra/packages/shared/pkg/proxy/pool"
)

const (
	trafficAccessTokenHeader = "e2b-traffic-access-token"
	proxyIdleTimeout         = 620 * time.Second
	proxyCacheSyncTimeout    = 30 * time.Second
)

type proxyResources interface {
	pod(context.Context, string) (*corev1.Pod, error)
	secret(context.Context, string) (*corev1.Secret, error)
}

type liveProxyResources struct {
	client    kubernetes.Interface
	namespace string
}

func (r liveProxyResources) pod(ctx context.Context, name string) (*corev1.Pod, error) {
	return r.client.CoreV1().Pods(r.namespace).Get(ctx, name, metav1.GetOptions{})
}

func (r liveProxyResources) secret(ctx context.Context, name string) (*corev1.Secret, error) {
	return r.client.CoreV1().Secrets(r.namespace).Get(ctx, name, metav1.GetOptions{})
}

type cachedProxyResources struct {
	pods    corelisters.PodNamespaceLister
	secrets corelisters.SecretNamespaceLister
}

func (r cachedProxyResources) pod(_ context.Context, name string) (*corev1.Pod, error) {
	return r.pods.Get(name)
}

func (r cachedProxyResources) secret(_ context.Context, name string) (*corev1.Secret, error) {
	return r.secrets.Get(name)
}

func newCachedProxyResources(ctx context.Context, client kubernetes.Interface, config Config) (proxyResources, error) {
	factory := informers.NewSharedInformerFactoryWithOptions(
		client,
		0,
		informers.WithNamespace(config.Namespace),
		informers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.LabelSelector = managedSandboxSelector()
		}),
	)
	pods := factory.Core().V1().Pods()
	secrets := factory.Core().V1().Secrets()
	podInformer := pods.Informer()
	secretInformer := secrets.Informer()
	factory.Start(ctx.Done())

	syncCtx, cancel := context.WithTimeout(ctx, proxyCacheSyncTimeout)
	defer cancel()
	if !cache.WaitForCacheSync(syncCtx.Done(), podInformer.HasSynced, secretInformer.HasSynced) {
		return nil, fmt.Errorf("sync sandbox proxy resource cache: %w", syncCtx.Err())
	}

	return cachedProxyResources{
		pods:    pods.Lister().Pods(config.Namespace),
		secrets: secrets.Lister().Secrets(config.Namespace),
	}, nil
}

// NewProxy creates the E2B data-plane reverse proxy. Unlike the Firecracker
// implementation, destinations are resolved from the current Pod IP on every
// request, so Pod recreation cannot reuse a stale address or connection pool.
func NewProxy(ctx context.Context, client kubernetes.Interface, config Config) (*reverseproxy.Proxy, error) {
	resources, err := newCachedProxyResources(ctx, client, config)
	if err != nil {
		return nil, err
	}
	getTargetFromRequest := reverseproxy.GetTargetFromRequest()
	return reverseproxy.New(
		config.ProxyPort,
		reverseproxy.SandboxProxyRetries,
		proxyIdleTimeout,
		func(request *http.Request) (*pool.Destination, error) {
			sandboxID, port, err := getTargetFromRequest(request)
			if err != nil {
				return nil, err
			}
			return proxyDestinationFromResources(request.Context(), resources, config, request, sandboxID, port)
		},
		nil,
		true,
	), nil
}

func proxyDestination(ctx context.Context, client kubernetes.Interface, config Config, request *http.Request, sandboxID string, port uint64) (*pool.Destination, error) {
	return proxyDestinationFromResources(ctx, liveProxyResources{client: client, namespace: config.Namespace}, config, request, sandboxID, port)
}

func proxyDestinationFromResources(ctx context.Context, resources proxyResources, config Config, request *http.Request, sandboxID string, port uint64) (*pool.Destination, error) {
	pod, err := resources.pod(ctx, podName(sandboxID))
	if err != nil || pod.Labels[managedByLabel] != managedByValue || pod.Labels[sandboxIDLabel] != sandboxID || !isPodReady(pod) || pod.Status.PodIP == "" {
		return nil, reverseproxy.NewErrSandboxNotFound(sandboxID)
	}

	secret, err := resources.secret(ctx, secretName(sandboxID))
	if err != nil || secret.Labels[managedByLabel] != managedByValue || secret.Labels[sandboxIDLabel] != sandboxID || secret.Annotations[identityAnnotation] != pod.Annotations[identityAnnotation] {
		return nil, reverseproxy.NewErrSandboxNotFound(sandboxID)
	}

	isNonEnvdTraffic := port != uint64(config.EnvdPort)
	if accessToken := secret.Data[secretTrafficTokenKey]; len(accessToken) > 0 && isNonEnvdTraffic {
		received := []byte(request.Header.Get(trafficAccessTokenHeader))
		if len(received) == 0 {
			return nil, reverseproxy.NewErrMissingTrafficAccessToken(sandboxID, trafficAccessTokenHeader)
		}
		if subtle.ConstantTimeCompare(received, accessToken) != 1 {
			return nil, reverseproxy.NewErrInvalidTrafficAccessToken(sandboxID, trafficAccessTokenHeader)
		}
	}

	var maskRequestHost *string
	if value := string(secret.Data[secretMaskHostKey]); value != "" && isNonEnvdTraffic {
		value = strings.ReplaceAll(value, pool.MaskRequestHostPortPlaceholder, strconv.FormatUint(port, 10))
		maskRequestHost = &value
	}

	connectionKey := string(pod.UID)
	if connectionKey == "" {
		connectionKey = pod.Annotations[identityAnnotation]
	}
	return &pool.Destination{
		Url: &url.URL{
			Scheme: "http",
			Host:   net.JoinHostPort(pod.Status.PodIP, strconv.FormatUint(port, 10)),
		},
		SandboxId:                          sandboxID,
		SandboxPort:                        port,
		DefaultToPortError:                 true,
		RequestLogger:                      logger.NewNopLogger(),
		ConnectionKey:                      connectionKey,
		IncludeSandboxIdInProxyErrorLogger: true,
		MaskRequestHost:                    maskRequestHost,
	}, nil
}
