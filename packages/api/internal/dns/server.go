package dns

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/redis/v8"
	resolver "github.com/miekg/dns"

	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const ttl = 0
const redisTTL = 24 * time.Hour

const defaultRoutingIP = "127.0.0.1"

const cachedDnsPrefix = "sandbox.dns."

type DNS struct {
	records *smap.Map[string]
	srv     *resolver.Server
	redis   *redis.Client

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

func (d *DNS) Add(ctx context.Context, sandboxID, ip string) {
	d.records.Insert(d.hostname(sandboxID), ip)
	if d.redis != nil {
		d.redis.Set(ctx, d.getCacheKey(sandboxID), ip, redisTTL)
	}
}

func (d *DNS) Remove(ctx context.Context, sandboxID, ip string) {
	d.records.RemoveCb(d.hostname(sandboxID), func(key string, v string, exists bool) bool {
		return v == ip
	})

	if d.redis != nil {
		d.redis.Del(ctx, d.getCacheKey(sandboxID))
	}
}

func (d *DNS) getLocal(hostname string) (string, bool) { return d.records.Get(hostname) }
func (*DNS) hostname(sandboxID string) string          { return fmt.Sprintf("%s.", sandboxID) }
func (*DNS) getCacheKey(id string) string              { return fmt.Sprintf("%s%s", cachedDnsPrefix, id) }

func (d *DNS) handleDNSRequest(ctx context.Context, w resolver.ResponseWriter, r *resolver.Msg) {
	m := new(resolver.Msg)
	m.SetReply(r)
	m.Compress = false
	m.Authoritative = true

	// TODO collect errors from redis, and log them.

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

			var ip net.IP

			sandboxID := strings.Split(q.Name, "-")[0]

			if addr, found := d.getLocal(sandboxID); found {
				// we have it cached locally, this is
				// still fine.
				a.A = net.ParseIP(addr).To4()
			} else if d.redis != nil {
				// TODO: at least log the error
				res, err := d.redis.Get(ctx, d.getCacheKey(sandboxID)).Result()
				if err == nil {
					// TODO: do we need to do
					// anything with the error or
					// distinguish between "can't
					// find" and "server error"

					ip = net.ParseIP(res)

					// TODO: should we add this to
					// the local cache at this
					// point or not?
				}
			}

			if ip == nil {
				ip = net.ParseIP(defaultRoutingIP)
			}

			a.A = ip.To4()
			m.Answer = append(m.Answer, a)
		}
	}

	err := w.WriteMsg(m)
	if err != nil {
		// TODO pass in a logger for clearer messages
		log.Printf("Failed to write message: %s\n", err.Error())
	}
}

var errOnStartup = errors.New("failed to start DNS server")

func CheckErrOnStartup(err error) bool { return errors.Is(err, errOnStartup) }

func (d *DNS) Start(ctx context.Context, address string, port int) {
	// It shuold be an error to call start twice. Potentially

	// configure the underlying resolver service.
	mux := resolver.NewServeMux()
	mux.HandleFunc(".", func(w resolver.ResponseWriter, r *resolver.Msg) { d.handleDNSRequest(ctx, w, r) })
	d.srv = &resolver.Server{Addr: fmt.Sprintf("%s:%d", address, port), Net: "udp", Handler: mux}

	// setup error handling here: we want to catch the error from
	// when the server starts.
	errChan := make(chan error)
	go func() {
		defer close(errChan)
		if err := d.srv.ListenAndServe(); err != nil {
			// don't do this against a context because we
			// want this to block until everything is shut
			// down for real, and the Close() method
			// should work.
			switch err.Error() {
			case "server already started", "bad network":
				errChan <- errors.Join(err, errOnStartup)
			default:
				errChan <- err
			}
		}
	}()

	// have to define this here so that we can avoid needing to
	// access the channel outside of this function (or need to).
	d.closer.op = func(ictx context.Context) error {
		select {
		case err := <-errChan:
			return err
		case <-ictx.Done():
			return ictx.Err()
		}
	}

	// have an extra go routine here that will trigger shutdown
	// when the start context is canceled.
	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Close should be a noop if it's already been called,
		// and it caches the error.
		_ = d.Close(shutdownCtx)
	}()
}

func (d *DNS) Close(ctx context.Context) error {
	if d.srv == nil {
		return errors.New("DNS was not started")
	}

	d.closer.once.Do(func() {
		var errs []error

		if err := d.srv.ShutdownContext(ctx); err != nil {
			errs = append(errs, err)
		}

		if err := d.closer.op(ctx); err != nil {
			switch err.Error() {
			case "server already started", "bad network":
				errs = append(errs, err, errOnStartup)
			default:
				errs = append(errs, err)
			}

		}

		d.closer.err = errors.Join(errs...)
	})

	return d.closer.err
}
