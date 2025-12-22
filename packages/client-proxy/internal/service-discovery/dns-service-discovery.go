package service_discovery

import (
	"context"
	"fmt"
	"time"

	"github.com/miekg/dns"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type DnsServiceDiscovery struct {
	logger   logger.Logger
	entries  *smap.Map[DiscoveredInstance]
	resolver string

	hosts       []string
	servicePort uint16
}

const (
	dnsMaxRetries = 3
	dnsRetryWait  = 5 * time.Millisecond

	cacheRefreshInterval = 10 * time.Second
)

var dnsClient = dns.Client{
	Net:     "udp",
	Timeout: time.Second * 2,
}

func NewDnsServiceDiscovery(ctx context.Context, logger logger.Logger, hosts []string, resolver string, servicePort uint16) *DnsServiceDiscovery {
	sd := &DnsServiceDiscovery{
		hosts:       hosts,
		logger:      logger,
		resolver:    resolver,
		servicePort: servicePort,

		entries: smap.New[DiscoveredInstance](),
	}

	go func() { sd.keepInSync(ctx) }()

	return sd
}

func (sd *DnsServiceDiscovery) ListInstances(_ context.Context) ([]DiscoveredInstance, error) {
	entries := sd.entries.Items()
	items := make([]DiscoveredInstance, 0)

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
			sd.logger.Info(ctx, "Stopping service discovery keep-in-sync")

			return
		case <-ticker.C:
			sd.sync(ctx)
		}
	}
}

func (sd *DnsServiceDiscovery) sync(ctx context.Context) {
	ctxTimeout, ctxCancel := context.WithTimeout(ctx, cacheRefreshInterval)
	defer ctxCancel()

	ips := make(map[string]struct{})

	select {
	case <-ctxTimeout.Done():
		sd.logger.Error(ctx, "Service discovery sync timed out")

		return
	default:
		for _, host := range sd.hosts {
			var msg dns.Msg
			msg.SetQuestion(dns.Fqdn(host), dns.TypeA)

			for range dnsMaxRetries {
				response, _, err := dnsClient.Exchange(&msg, sd.resolver)
				if err != nil {
					sd.logger.Error(ctx, "DNS service discovery failed", zap.Error(err))
					time.Sleep(dnsRetryWait)

					continue
				}

				for _, ans := range response.Answer {
					if rr, ok := ans.(*dns.A); ok {
						ips[rr.A.String()] = struct{}{}
					}
				}

				break
			}
		}
	}

	// create or update the entries
	for ip := range ips {
		key := fmt.Sprintf("%s:%d", ip, sd.servicePort)
		sd.entries.Insert(
			key, DiscoveredInstance{InstanceIPAddress: ip, InstancePort: sd.servicePort},
		)
	}

	// remove entries that are no longer in DNS response
	for key, item := range sd.entries.Items() {
		if _, ok := ips[item.InstanceIPAddress]; !ok {
			sd.entries.Remove(key)
		}
	}
}
