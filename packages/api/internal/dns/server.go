package dns

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"

	"github.com/go-redis/redis/v8"
	resolver "github.com/miekg/dns"

	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const ttl = 0

// This allows us to return a different error message when the sandbox is not found instead of generic 502 Bad Gateway
const defaultRoutingIP = "127.0.0.1"

type DNS struct {
	mu      sync.Mutex
	redis   *redis.Client
	records *smap.Map[string]
}

func New(rc *redis.Client) *DNS {
	return &DNS{
		redis:   rc,
		records: smap.New[string](),
	}
}

func (d *DNS) Add(_ context.Context, sandboxID, ip string) {
	d.records.Insert(d.hostname(sandboxID), ip)
}

func (d *DNS) Remove(_ context.Context, sandboxID, ip string) {
	d.records.RemoveCb(d.hostname(sandboxID), func(key string, v string, exists bool) bool {
		return v == ip
	})
}

func (d *DNS) get(hostname string) (string, bool) {
	return d.records.Get(hostname)
}

func (*DNS) hostname(sandboxID string) string {
	return fmt.Sprintf("%s.", sandboxID)
}

func (d *DNS) handleDNSRequest(w resolver.ResponseWriter, r *resolver.Msg) {
	m := new(resolver.Msg)
	m.SetReply(r)
	m.Compress = false
	m.Authoritative = true

	for _, q := range m.Question {
		if q.Qtype == resolver.TypeA {
			a := &resolver.A{
				Hdr: resolver.RR_Header{
					Name:   q.Name,
					Rrtype: resolver.TypeA,
					Class:  resolver.ClassINET,
					Ttl:    ttl,
				},
			}

			sandboxID := strings.Split(q.Name, "-")[0]
			ip, found := d.get(sandboxID)
			if found {
				a.A = net.ParseIP(ip).To4()
			} else {
				a.A = net.ParseIP(defaultRoutingIP).To4()
			}

			m.Answer = append(m.Answer, a)
		}
	}

	err := w.WriteMsg(m)
	if err != nil {
		log.Printf("Failed to write message: %s\n", err.Error())
	}
}

func (d *DNS) Start(_ context.Context, address string, port string) error {
	mux := resolver.NewServeMux()

	mux.HandleFunc(".", d.handleDNSRequest)

	server := resolver.Server{Addr: fmt.Sprintf("%s:%s", address, port), Net: "udp", Handler: mux}

	err := server.ListenAndServe()
	if err != nil {
		return fmt.Errorf("failed to start DNS server: %w", err)
	}

	return nil
}
