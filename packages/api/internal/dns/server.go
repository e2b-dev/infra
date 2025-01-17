package dns

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	redis "github.com/go-redis/redis/v8"
	resolver "github.com/miekg/dns"
	"go.uber.org/zap"
)

const redisExpirationTime = time.Hour * 24

const ttl = 0

const defaultRoutingIP = "127.0.0.1"

type FallbackResolverFn = func(sandboxID string) (string, bool)

type DNS struct {
	ctx                context.Context
	rdb                *redis.Client
	fallbackResolverFn FallbackResolverFn
	logger             *zap.SugaredLogger
}

func New(ctx context.Context, rdbOpts *redis.Options, fallbackResolverFn FallbackResolverFn, logger *zap.SugaredLogger) *DNS {
	return &DNS{
		ctx:                ctx,
		rdb:                redis.NewClient(rdbOpts),
		fallbackResolverFn: fallbackResolverFn,
		logger:             logger,
	}
}

func (d *DNS) Add(sandboxID, ip string) error {
	d.logger.Infof("DNS: Adding entry, sandboxID=%s -> %s", sandboxID, ip)
	if err := d.rdb.Set(d.ctx, d.dnsKeyFor(sandboxID), ip, redisExpirationTime).Err(); err != nil {
		return err
	}
	return nil
}

func (d *DNS) Remove(sandboxID string) error {
	d.logger.Infof("DNS: Removing entry, sandboxID=%s", sandboxID)
	if err := d.rdb.Del(d.ctx, d.dnsKeyFor(sandboxID)).Err(); err != nil {
		return err
	}
	return nil
}

func (d *DNS) get(sandboxID string) (string, bool) {
	res, err := d.rdb.Get(d.ctx, d.dnsKeyFor(sandboxID)).Result()
	if err == nil {
		return res, true
	}
	if err != redis.Nil {
		d.logger.Warnf("DNS: Redis error getting key for sandbox '%s' (will try fallback resolver..): %s", sandboxID, err)
	}

	if d.fallbackResolverFn != nil {
		if rec, ok := d.fallbackResolverFn(sandboxID); ok {
			d.logger.Infof("DNS: Not found in redis, using fallback lookup for sandbox '%s' succeeded: record=%q", sandboxID, rec)
			go func() {
				if err := d.Add(sandboxID, rec); err != nil {
					d.logger.Errorf("DNS: Problem adding entry: %s", err)
				}
			}()
			return rec, true
		} else {
			d.logger.Errorf("DNS: Fallback lookup for sandbox '%s' failed", sandboxID)
		}
	}
	return "", false
}

func (d *DNS) dnsKeyFor(sandboxID string) string {
	return fmt.Sprintf("dns.%s", sandboxID)
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
			// Trim trailing period to facilitate key consistency.
			sandboxID = strings.TrimSuffix(sandboxID, ".")

			if ip, found := d.get(sandboxID); found {
				a.A = net.ParseIP(ip).To4()
			} else {
				a.A = net.ParseIP(defaultRoutingIP).To4()
			}

			m.Answer = append(m.Answer, a)
		}
	}

	err := w.WriteMsg(m)
	if err != nil {
		d.logger.Errorf("DNS: Failed to write message: %w", err)
	}
}

func (d *DNS) Start(address string, port int) error {
	mux := resolver.NewServeMux()

	mux.HandleFunc(".", d.handleDNSRequest)

	server := resolver.Server{
		Addr:    fmt.Sprintf("%s:%d", address, port),
		Net:     "udp",
		Handler: mux,
	}

	err := server.ListenAndServe()
	if err != nil {
		return fmt.Errorf("DNS: failed to start server: %w", err)
	}

	return nil
}
