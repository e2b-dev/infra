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

// This allows us to return a different error message when the sandbox is not found instead of generic 502 Bad Gateway
const defaultRoutingIP = "127.0.0.1"

const cachedDnsPrefix = "sandbox.dns."

type DNS struct {
	srv *resolver.Server

	remote  *cache.Cache
	remote2 *cache.Cache
	local   *smap.Map[string]

	closer struct {
		once sync.Once
		op   func(context.Context) error
		err  error
	}
}

func New(ctx context.Context, rc *redis.Client, rc2 *redis.Client) *DNS {
	d := &DNS{}

	if rc != nil {
		d.remote = cache.New(&cache.Options{Redis: rc, LocalCache: cache.NewTinyLFU(10_000, time.Hour)})
		if rc2 != nil {
			// No need for local cache, we never read from this redis
			d.remote2 = cache.New(&cache.Options{Redis: rc2})
		}
	} else {
		d.local = smap.New[string]()
	}

	return d
}

func (d *DNS) Add(ctx context.Context, sandboxID, ip string) {
	switch {
	case d.remote != nil:
		err := d.remote.Set(&cache.Item{
			Ctx:   ctx,
			TTL:   redisTTL,
			Key:   d.cacheKey(sandboxID),
			Value: ip,
		})
		if err != nil {
			zap.L().Warn("adding item to DNS cache", zap.Error(err), zap.String("sandbox_id", sandboxID))
		}
		if d.remote2 != nil {
			d.remote2.Set(&cache.Item{
				Ctx:   ctx,
				TTL:   redisTTL,
				Key:   d.cacheKey(sandboxID),
				Value: ip,
			})
		}
	case d.local != nil:
		d.local.Insert(sandboxID, ip)
	}
}

func (d *DNS) Remove(ctx context.Context, sandboxID, ip string) {
	switch {
	case d.remote != nil:
		if err := d.remote.Delete(ctx, d.cacheKey(sandboxID)); err != nil {
			zap.L().Debug("removing item from DNS cache", zap.Error(err), zap.String("sandbox_id", sandboxID))
		}
		if d.remote2 != nil {
			if err := d.remote2.Delete(ctx, d.cacheKey(sandboxID)); err != nil {
				zap.L().Debug("removing item from 2nd DNS cache", zap.Error(err), zap.String("sandbox_id", sandboxID))
			}
		}
	case d.local != nil:
		d.local.RemoveCb(d.cacheKey(sandboxID), func(k string, v string, ok bool) bool { return v == ip })
	}
}

func (d *DNS) Get(ctx context.Context, sandboxID string) net.IP {
	var res string
	switch {
	case d.remote != nil:
		if err := d.remote.Get(ctx, d.cacheKey(sandboxID), &res); err != nil {
			if errors.Is(err, cache.ErrCacheMiss) {
				zap.L().Warn("item missing in remote DNS cache", zap.String("sandbox_id", sandboxID))
				if d.remote2 != nil {
					if err := d.remote2.Get(ctx, d.cacheKey(sandboxID), &res); err != nil {
						if errors.Is(err, cache.ErrCacheMiss) {
							zap.L().Debug("item missing in 2nd remote DNS cache", zap.String("sandbox_id", sandboxID))
						} else {
							zap.L().Error("resolving item from remote DNS cache", zap.String("sandbox_id", sandboxID), zap.Error(err))
						}
					}
				}
			} else {
				zap.L().Error("resolving item from remote DNS cache", zap.String("sandbox_id", sandboxID), zap.Error(err))
			}
		}
	case d.local != nil:
		var ok bool
		res, ok = d.local.Get(d.cacheKey(sandboxID))
		if !ok {
			zap.L().Warn("item not found in local DNS cache", zap.String("sandbox", sandboxID))
		}
	}

	addr := net.ParseIP(res)
	if addr == nil {
		if res != "" {
			zap.L().Error("malformed address in cache", zap.Bool("local", d.local != nil), zap.String("addr", res))
		}

		addr = net.ParseIP(defaultRoutingIP)
	}

	return addr.To4()
}

func (d *DNS) cacheKey(id string) string {
	switch {
	case d.remote != nil:
		// add a prefix to the remote cache items to make is
		// reasonable to introspect the remote cache data, to
		// make it possible to safely use the redis cache for
		// more than one set of cached items without fear of
		// collision. Additionally the prefix allows us to
		// have a hard break of compatibility between versions
		// of the service by changing the prefix.
		return fmt.Sprintf("%s%s", cachedDnsPrefix, id)
	default:
		// local caches are scoped to the `DNS` instance and so don't need a prefix.
		return id
	}
}

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
				A: d.Get(ctx, strings.TrimSuffix(sandboxID, ".")),
			})
		}
	}

	if err := w.WriteMsg(m); err != nil {
		zap.L().Error("write DNS message", zap.Error(err))
	}
}

var errOnStartup = errors.New("failed to start DNS server")

func CheckErrOnStartup(err error) bool { return errors.Is(err, errOnStartup) }

func (d *DNS) Start(ctx context.Context, address string, port string) {
	if d.srv != nil {
		return
	}

	// configure the underlying resolver service.
	mux := resolver.NewServeMux()
	mux.HandleFunc(".", func(w resolver.ResponseWriter, r *resolver.Msg) { d.handleDNSRequest(ctx, w, r) })
	d.srv = &resolver.Server{Addr: fmt.Sprintf("%s:%s", address, port), Net: "udp", Handler: mux}

	// setup error handling here: we want to catch the error from
	// when the server starts.
	errChan := make(chan error, 1)
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
				panic(errors.Join(errors.New("configuration problem with DNS service"), err, errOnStartup))
			case "server already started":
				// this only happens if you call start
				// more than once, which shouldn't be
				// possible.
				errChan <- errors.Join(err, errOnStartup)
			default:
				// encounter a non-nil error when listening
				//
				// this should only happen if we
				// encounter a (networking(?)) error
				// during operation. Panic so that the
				// service aborts rather than
				// continuing in an unhealty state.
				panic(errors.Join(errors.New("DNS service error"), err))
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

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// Close should be a noop if it's already been called,
		// and it caches the error.
		_ = d.Close(shutdownCtx)
	}()
}

func (d *DNS) Close(ctx context.Context) error {
	if d.srv == nil {
		return nil
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
