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

	query       string
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

func NewDnsServiceDiscovery(ctx context.Context, query string, servicePort int, logger *zap.Logger) *DnsServiceDiscovery {
	sd := &DnsServiceDiscovery{
		query:   query,
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

	select {
	case <-ctxTimeout.Done():
		sd.logger.Error("Service discovery sync timed out")
		return
	default:
		for _ = range dnsMaxRetries {
			ips, err := dnsResolver.LookupIP(ctxTimeout, "ip", sd.query)
			if err != nil {
				sd.logger.Error("DNS service discovery failed", zap.Error(err))
				time.Sleep(dnsRetryWait)
				continue
			}

			sd.logger.Debug("DNS service discovery response", zap.Int("ips", len(ips)))

			// Map IPs
			ipsMap := make(map[string]string)
			for _, ip := range ips {
				ipsMap[ip.String()] = ip.String()
			}

			// Create or update the entries
			for _, answer := range ips {
				ip := answer.String()
				key := fmt.Sprintf("%s:%d", ip, sd.servicePort)

				sd.entries.Insert(
					key, &ServiceDiscoveryItem{NodeIp: ip, NodePort: sd.servicePort},
				)
			}

			// Remove entries that are no longer in DNS response
			for key, item := range sd.entries.Items() {
				if _, ok := ipsMap[item.NodeIp]; !ok {
					sd.entries.Remove(key)
				}
			}

			break
		}
	}
}
