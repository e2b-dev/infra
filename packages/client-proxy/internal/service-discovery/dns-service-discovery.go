package service_discovery

import (
	"context"
	"fmt"
	"net"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type DnsServiceDiscovery struct {
	logger  *zap.Logger
	entries *smap.Map[*ServiceDiscoveryItem]

	hosts       []string
	servicePort int
}

const (
	dnsMaxRetries = 3
	dnsRetryWait  = 5 * time.Millisecond

	cacheRefreshInterval = 10 * time.Second
)

var (
	dnsResolver = net.DefaultResolver
)

func NewDnsServiceDiscovery(ctx context.Context, hosts []string, servicePort int, logger *zap.Logger) *DnsServiceDiscovery {
	sd := &DnsServiceDiscovery{
		hosts:   hosts,
		logger:  logger,
		entries: smap.New[*ServiceDiscoveryItem](),

		servicePort: servicePort,
	}

	go func() { sd.keepInSync(ctx) }()

	return sd
}

func (sd *DnsServiceDiscovery) ListNodes(_ context.Context) ([]*ServiceDiscoveryItem, error) {
	entries := sd.entries.Items()
	items := make([]*ServiceDiscoveryItem, 0)

	for _, item := range entries {
		items = append(items, item)
	}

	return items, nil
}

func (sd *DnsServiceDiscovery) keepInSync(ctx context.Context) {
	// Run the first sync immediately
	sd.sync(ctx)

	ticker := time.NewTicker(cacheRefreshInterval)
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

func (sd *DnsServiceDiscovery) sync(ctx context.Context) {
	ctxTimeout, ctxCancel := context.WithTimeout(ctx, cacheRefreshInterval)
	defer ctxCancel()

	ips := make(map[string]string)

	select {
	case <-ctxTimeout.Done():
		sd.logger.Error("Service discovery sync timed out")
		return
	default:
		for _, host := range sd.hosts {
			for range dnsMaxRetries {
				hostIps, err := dnsResolver.LookupIP(ctxTimeout, "ip", host)
				if err != nil {
					sd.logger.Error("DNS service discovery failed", zap.Error(err))
					time.Sleep(dnsRetryWait)
					continue
				}

				for _, ip := range hostIps {
					ips[ip.String()] = ip.String()
				}

				break
			}
		}
	}

	// create or update the entries
	for _, ip := range ips {
		key := fmt.Sprintf("%s:%d", ip, sd.servicePort)
		sd.entries.Insert(
			key, &ServiceDiscoveryItem{NodeIp: ip, NodePort: sd.servicePort},
		)
	}

	// remove entries that are no longer in DNS response
	for key, item := range sd.entries.Items() {
		if _, ok := ips[item.NodeIp]; !ok {
			sd.entries.Remove(key)
		}
	}
}
