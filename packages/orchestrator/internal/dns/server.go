package dns

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"

	resolver "github.com/miekg/dns"

	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const ttl = 0

const defaultRoutingIP = "127.0.0.1"
const defaultErrorPort = 3003

type SandboxErrorChecker func(sandboxID string) error

type OrchDNS struct {
	mu                  sync.Mutex
	records             *smap.Map[string]
	sandboxErrorChecker SandboxErrorChecker
}

func New(sandboxErrorChecker SandboxErrorChecker) *OrchDNS {
	return &OrchDNS{
		records:             smap.New[string](),
		sandboxErrorChecker: sandboxErrorChecker,
	}
}

func (d *OrchDNS) Add(sandboxID, ip string) {
	d.records.Insert(d.hostname(sandboxID), ip)
}

func (d *OrchDNS) Remove(sandboxID, ip string) {
	d.records.RemoveCb(d.hostname(sandboxID), func(key string, v string, exists bool) bool {
		return v == ip
	})
}

func (d *OrchDNS) get(hostname string) (string, bool) {
	return d.records.Get(hostname)
}

func (*OrchDNS) hostname(sandboxID string) string {
	return fmt.Sprintf("%s.", sandboxID)
}

func (d *OrchDNS) handleDNSRequest(w resolver.ResponseWriter, r *resolver.Msg) {
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
				err := d.sandboxErrorChecker(sandboxID)
				if err != nil {
					a.A = net.ParseIP(defaultRoutingIP).To4()
				}
			}
		}
	}

	err := w.WriteMsg(m)
	if err != nil {
		log.Printf("Failed to write message: %s\n", err.Error())
	}
}

func (d *OrchDNS) Start(address string, port int) error {
	mux := resolver.NewServeMux()

	mux.HandleFunc(".", d.handleDNSRequest)

	server := resolver.Server{Addr: fmt.Sprintf("%s:%d", address, port), Net: "udp", Handler: mux}

	err := server.ListenAndServe()
	if err != nil {
		return fmt.Errorf("failed to start DNS server: %w", err)
	}

	err = d.startErrorServer(defaultRoutingIP, defaultErrorPort)
	if err != nil {
		return fmt.Errorf("failed to start error HTTP server: %w", err)
	}

	return nil
}

func (d *OrchDNS) startErrorServer(address string, port int) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(r.Host, "-")
		sandboxID := ""
		if len(parts) >= 2 {
			sandboxID = parts[1]
		}

		errMsg := "Sandbox does not exist."

		if err := d.sandboxErrorChecker(sandboxID); err != nil {
			errMsg = err.Error()
		}

		w.WriteHeader(http.StatusBadGateway)
		w.Write([]byte(errMsg))
	})

	server := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", address, port),
		Handler: mux,
	}

	err := server.ListenAndServe()
	if err != nil {
		return fmt.Errorf("failed to start error HTTP server: %w", err)
	}

	return nil
}
