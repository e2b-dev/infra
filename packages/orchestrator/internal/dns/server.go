package dns

import (
	"fmt"
	"log"
	"net"
	"strings"
	"sync"

	resolver "github.com/miekg/dns"

	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const ttl = 0

type DNS struct {
	mu      sync.Mutex
	records *smap.Map[string]
}

func New() *DNS {
	return &DNS{
		records: smap.New[string](),
	}
}

func (d *DNS) Add(sandboxID, ip string) {
	d.records.Insert(d.hostname(sandboxID), ip)
}

func (d *DNS) Remove(sandboxID, ip string) {
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
			sandboxID := strings.Split(q.Name, "-")[0]
			ip, found := d.get(sandboxID)
			if found {
				a := &resolver.A{
					Hdr: resolver.RR_Header{
						Name:   q.Name,
						Rrtype: resolver.TypeA,
						Class:  resolver.ClassINET,
						Ttl:    ttl,
					},
					A: net.ParseIP(ip).To4(),
				}

				m.Answer = append(m.Answer, a)
			}
		}
	}

	err := w.WriteMsg(m)
	if err != nil {
		log.Printf("Failed to write message: %s\n", err.Error())
	}
}

func (d *DNS) Start(address string, port int) error {
	mux := resolver.NewServeMux()

	mux.HandleFunc(".", d.handleDNSRequest)

	server := resolver.Server{Addr: fmt.Sprintf("%s:%d", address, port), Net: "udp", Handler: mux}

	err := server.ListenAndServe()
	if err != nil {
		return fmt.Errorf("failed to start DNS server: %w", err)
	}

	return nil
}
