package dns

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/go-redis/cache/v8"
	"github.com/go-redis/redis/v8"
	resolver "github.com/miekg/dns"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

const ttl = 0
const redisTTL = 24 * time.Hour

const defaultRoutingIP = "127.0.0.1"

const cachedDnsPrefix = "sandbox.dns."

type DNS struct {
	srv    *resolver.Server
	logger *zap.SugaredLogger

	remote *cache.Cache
	local  *smap.Map[string]

	closer struct {
		once sync.Once
		op   func(context.Context) error
		err  error
	}
}

func New(ctx context.Context, rc *redis.Client, logger *zap.SugaredLogger) *DNS {
	d := &DNS{logger: logger}

	if rc != nil {
		d.remote = cache.New(&cache.Options{Redis: rc, LocalCache: cache.NewTinyLFU(10_000, time.Hour)})
	} else {
		d.local = smap.New[string]()
	}

	return d
}

func (d *DNS) Add(ctx context.Context, sandboxID, ip string) {
	switch {
	case d.remote != nil:
		d.remote.Set(&cache.Item{
			Ctx:   ctx,
			TTL:   redisTTL,
			Key:   sandboxID,
			Value: ip,
		})
	case d.local != nil:
		d.local.Insert(sandboxID, ip)
	default:
		d.logger.Panic("malformed DNS service")
	}
}

func (d *DNS) Remove(ctx context.Context, sandboxID, ip string) {
	switch {
	case d.remote != nil:
		if err := d.remote.Delete(ctx, sandboxID); err != nil {
			d.logger.Debug("removing item from DNS cache", zap.Error(err), zap.String("sandbox", sandboxID))
		}
	case d.local != nil:
		d.local.RemoveCb(d.hostname(sandboxID), func(k string, v string, ok bool) bool {
			return v == ip
		})
	default:
		d.logger.Panic("malformed DNS service")
	}
}

func (d *DNS) Get(ctx context.Context, sandboxID string) net.IP {
	var res string
	switch {
	case d.remote != nil:
		if err := d.remote.Get(ctx, sandboxID, &res); err != nil {
			if errors.Is(err, cache.ErrCacheMiss) {
				d.logger.Warn("item missing in remote DNS cache", zap.String("sandbox", sandboxID))
			} else {
				d.logger.Error("resolving item from remote DNS cache", zap.String("sandbox", sandboxID), zap.Error(err))
			}
		}
	case d.local != nil:
		var ok bool
		res, ok = d.local.Get(sandboxID)
		if !ok {
			d.logger.Warn("item not found in local DNS cache", zap.String("sandbox", sandboxID))
		}
	}

	addr := net.ParseIP(res)
	if addr == nil {
		if res != "" {
			d.logger.Error("malformed address in cache", zap.Bool("local", d.local != nil), zap.String("addr", res))
		}

		addr = net.ParseIP(defaultRoutingIP)
	}

	return addr.To4()
}

func (*DNS) hostname(sandboxID string) string { return fmt.Sprintf("%s.", sandboxID) }
func (*DNS) getCacheKey(id string) string     { return fmt.Sprintf("%s%s", cachedDnsPrefix, id) }

func (d *DNS) handleDNSRequest(ctx context.Context, w resolver.ResponseWriter, r *resolver.Msg) {
	m := &resolver.Msg{
		Compress: false,
		MsgHdr: resolver.MsgHdr{
			Authoritative: true,
		},
	}

	m.SetReply(r)

	for _, q := range m.Question {
		if q.Qtype == resolver.TypeA {
			sandboxID := strings.Split(q.Name, "-")[0]

			m.Answer = append(m.Answer, &resolver.A{
				Hdr: resolver.RR_Header{
					Name:   q.Name,
					Rrtype: resolver.TypeA,
					Class:  resolver.ClassINET,
					Ttl:    ttl,
				},
				A: d.Get(ctx, sandboxID),
			})
		}
	}

	if err := w.WriteMsg(m); err != nil {
		d.logger.Error("write DNS message", zap.Error(err))
	}
}

var errOnStartup = errors.New("failed to start DNS server")

func CheckErrOnStartup(err error) bool { return errors.Is(err, errOnStartup) }

func (d *DNS) Start(ctx context.Context, address string, port int) {
	if d.srv != nil {
		return
	}

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
			case "bad network":
				// this is the only error that can
				// happen during startup. We have to
				// panic here because we don't want
				// the service to continue without any
				// DNS service.
				panic(errors.Join(errors.New("problem starting DNS service"), err, errOnStartup))
			case "server already started":
				// this only happens if you call start
				// more than once, which shouldn't be
				// possible.
				errChan <- errors.Join(err, errOnStartup)
			default:
				// this should only happen if we
				// encounter a (networking(?)) error
				// during operation.

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
			errs = append(errs, err)
		}

		d.closer.err = errors.Join(errs...)
	})

	return d.closer.err
}
