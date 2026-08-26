package servicediscovery

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
	entries  *smap.Map[Instance]
	resolver string

	hosts       []string
	servicePort uint16

	cancel func()
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

func NewDnsServiceDiscovery(logger logger.Logger, hosts []string, resolver string, servicePort uint16) *DnsServiceDiscovery {
	sd := &DnsServiceDiscovery{
		hosts:       hosts,
		logger:      logger,
		resolver:    resolver,
		servicePort: servicePort,

		entries: smap.New[Instance](),
		cancel:  func() {},
	}

	return sd
}

func (sd *DnsServiceDiscovery) Start(ctx context.Context) {
	ctx, sd.cancel = context.WithCancel(ctx)

	go sd.keepInSync(ctx)
}

func (sd *DnsServiceDiscovery) Stop(context.Context) {
	sd.cancel()
}

func (sd *DnsServiceDiscovery) ListInstances(_ context.Context) ([]Instance, error) {
	entries := sd.entries.Items()
	items := make([]Instance, 0)

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
			key, Instance{WorkloadID: key, IPAddress: ip, Port: sd.servicePort},
		)
	}

	// remove entries that are no longer in DNS response
	for key, item := range sd.entries.Items() {
		if _, ok := ips[item.IPAddress]; !ok {
			sd.entries.Remove(key)
		}
	}
}
