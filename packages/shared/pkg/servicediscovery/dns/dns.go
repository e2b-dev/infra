package dns

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/miekg/dns"
	"go.opentelemetry.io/otel"

	"github.com/e2b-dev/infra/packages/shared/pkg/servicediscovery"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/shared/pkg/servicediscovery/dns")

const (
	dnsMaxRetries = 3
	dnsRetryWait  = 5 * time.Millisecond
)

var dnsClient = dns.Client{Net: "udp", Timeout: 2 * time.Second}

// dnsDiscovery resolves a set of hostnames to the addresses behind them. It
// reports a failure when no host resolved: previously a total DNS outage
// emptied the set and reported success, which read as a fleet with nothing on
// it.
type dnsDiscovery struct {
	servicediscovery.NoSync

	hosts       []string
	resolver    string
	servicePort uint16
}

// New creates a servicediscovery.Discoverer over the A records of hosts, resolved through
// resolver. Wrap it in servicediscovery.Cached to serve it from a background refresh.
func New(hosts []string, resolver string, servicePort uint16) servicediscovery.Discoverer {
	return &dnsDiscovery{hosts: hosts, resolver: resolver, servicePort: servicePort}
}

func (d *dnsDiscovery) ListInstances(ctx context.Context) ([]servicediscovery.Instance, error) {
	ctx, span := tracer.Start(ctx, "list-dns-instances")
	defer span.End()

	seen := make(map[string]struct{})
	out := make([]servicediscovery.Instance, 0)

	var errs []error
	for _, host := range d.hosts {
		addresses, err := d.resolve(ctx, host)
		if err != nil {
			errs = append(errs, err)

			continue
		}

		for _, ip := range addresses {
			if _, ok := seen[ip]; ok {
				continue
			}
			seen[ip] = struct{}{}

			out = append(out, servicediscovery.Instance{WorkloadID: fmt.Sprintf("%s:%d", ip, d.servicePort), IPAddress: ip, Port: d.servicePort})
		}
	}

	// A partial resolution is still a usable set; only a total failure means we
	// learned nothing and must not be read as "nothing is there".
	if len(out) == 0 && len(errs) > 0 {
		return nil, fmt.Errorf("resolving every discovery host failed: %w", errors.Join(errs...))
	}

	return out, nil
}

func (d *dnsDiscovery) resolve(ctx context.Context, host string) ([]string, error) {
	var msg dns.Msg
	msg.SetQuestion(dns.Fqdn(host), dns.TypeA)

	var err error
	for attempt := range dnsMaxRetries {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(dnsRetryWait):
			}
		}

		var response *dns.Msg
		response, _, err = dnsClient.Exchange(&msg, d.resolver)
		if err != nil {
			continue
		}

		// A resolver that answers SERVFAIL or REFUSED carries no error and no
		// answers, so without this the outage reads as "this service has no
		// instances" and the caller reconciles the fleet away. NOERROR with an
		// empty answer section still means exactly that, and is left alone.
		if response.Rcode != dns.RcodeSuccess {
			err = fmt.Errorf("resolver answered %s", dns.RcodeToString[response.Rcode])

			continue
		}

		out := make([]string, 0, len(response.Answer))
		for _, answer := range response.Answer {
			if record, ok := answer.(*dns.A); ok {
				out = append(out, record.A.String())
			}
		}

		return out, nil
	}

	return nil, fmt.Errorf("resolving %q: %w", host, err)
}
