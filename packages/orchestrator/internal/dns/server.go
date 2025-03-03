package dns

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"

	resolver "github.com/miekg/dns"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const ttl = 0

type DNS struct {
	records *smap.Map[string]

	closer struct {
		once sync.Once
		op   func(context.Context) error
		err  error
	}
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
		zap.L().Error("failed to write message", zap.Error(err))
	}
}

func (d *DNS) Start(address string, port int) error {
	mux := resolver.NewServeMux()

	mux.HandleFunc(".", d.handleDNSRequest)

	server := resolver.Server{Addr: fmt.Sprintf("%s:%d", address, port), Net: "udp", Handler: mux}

	if err := server.ListenAndServe(); err != nil {
		return fmt.Errorf("DNS server encounterted error: %w", err)
	}

	d.closer.op = server.ShutdownContext

	return nil
}

func (d *DNS) Close(ctx context.Context) error {
	d.closer.once.Do(func() { d.closer.err = d.closer.op(ctx) })
	return d.closer.err
}
