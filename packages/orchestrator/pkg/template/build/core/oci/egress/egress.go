// Package egress provides an HTTP transport whose dialer blocks connections to
// internal/private IP addresses, with an allowlist override. It borrows the
// internal-address denylist from the sandbox-network package but is otherwise
// independent of sandbox networking.
package egress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
)

// ErrBlocked is wrapped into the dial error when a connection targets an
// internal/private address that is not covered by the allowlist, so callers can
// detect it with errors.Is.
var ErrBlocked = errors.New("connection to an internal or private address is not allowed")

// dialTimeout and dialKeepAlive match go-containerregistry's default transport
// so replacing the dialer does not change connection behaviour.
const (
	dialTimeout   = 30 * time.Second
	dialKeepAlive = 30 * time.Second
)

// Allowlist holds parsed allowlist entries: IP prefixes matched against the
// resolved connection IP, and domain patterns matched against the dialed
// hostname (exact, "*", or "*.suffix").
type Allowlist struct {
	prefixes []netip.Prefix
	domains  []string
}

// ParseAllowlist classifies each entry as an IP/CIDR or a domain pattern. A bare
// IP becomes a single-host prefix. Entries that are neither a valid IP nor CIDR
// are treated as domain patterns.
func ParseAllowlist(entries []string) Allowlist {
	var allow Allowlist

	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		if prefix, err := netip.ParsePrefix(entry); err == nil {
			allow.prefixes = append(allow.prefixes, prefix.Masked())

			continue
		}

		if addr, err := netip.ParseAddr(entry); err == nil {
			allow.prefixes = append(allow.prefixes, netip.PrefixFrom(addr, addr.BitLen()))

			continue
		}

		allow.domains = append(allow.domains, entry)
	}

	return allow
}

// permitsIP reports whether a connection to the resolved addr is allowed: either
// the IP is explicitly allowlisted, or it is not in the internal-address
// denylist. Allowlist entries take precedence, so an operator can re-enable an
// otherwise-denied range (e.g. 127.0.0.0/8 for local development).
func (a Allowlist) permitsIP(addr netip.Addr) bool {
	addr = addr.Unmap()

	for _, prefix := range a.prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}

	// Use the 16-byte representation for a consistent net.IP regardless of
	// address family.
	ip16 := addr.As16()

	return !sandbox_network.IsIPInDeniedSandboxCIDRs(net.IP(ip16[:]))
}

// permitsHost reports whether the dialed hostname matches an allowlisted domain
// pattern.
func (a Allowlist) permitsHost(host string) bool {
	for _, pattern := range a.domains {
		if matchDomain(host, pattern) {
			return true
		}
	}

	return false
}

// matchDomain reports whether hostname matches a domain pattern: an exact
// (case-insensitive) match, "*", or a "*.suffix" wildcard.
func matchDomain(hostname, pattern string) bool {
	switch {
	case pattern == "":
		return false
	case pattern == "*":
		return true
	case strings.EqualFold(pattern, hostname):
		return true
	case strings.HasPrefix(pattern, "*."):
		suffix := pattern[1:] // ".suffix"

		return strings.HasSuffix(strings.ToLower(hostname), strings.ToLower(suffix))
	}

	return false
}

// NewTransport returns a clone of base whose dialer rejects connections to
// internal/private IPs unless allowed by allow. A connection is permitted when
// the dialed hostname matches an allowlisted domain pattern, or when the
// resolved IP passes the allowlist/denylist check. The IP check runs in the
// dialer's ControlContext callback, after DNS resolution but before the TCP
// connect() syscall. onBlocked, when set, is invoked with the blocked IP for
// telemetry.
//
// base preserves the caller's transport tuning (TLS, proxy, idle-connection
// settings); only the DialContext is replaced. A nil base falls back to
// http.DefaultTransport.
//
// If a proxy applies to registryHost (e.g. via HTTP_PROXY/HTTPS_PROXY, honoring
// NO_PROXY), connections are routed through it and the guard is not applied: a
// configured proxy takes precedence over the allowlist/denylist.
func NewTransport(base *http.Transport, registryHost string, allow Allowlist, onBlocked func(netip.Addr)) *http.Transport {
	if base == nil {
		dt, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			dt = &http.Transport{}
		}

		base = dt
	}

	t := base.Clone()

	// A proxy that applies to the registry takes precedence: connections are
	// tunneled through it, so the dialer never sees the registry's IP and the
	// guard does not apply.
	if proxyConfigured(t.Proxy, registryHost) {
		return t
	}

	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}

		dialer := &net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: dialKeepAlive,
			// ControlContext runs after DNS resolution but before connect(); the
			// address argument is the resolved IP:port.
			ControlContext: func(_ context.Context, _, address string, _ syscall.RawConn) error {
				if allow.permitsHost(host) {
					return nil
				}

				addrPort, err := netip.ParseAddrPort(address)
				if err != nil {
					return fmt.Errorf("failed to parse resolved address %q: %w", address, err)
				}

				resolved := addrPort.Addr()
				if allow.permitsIP(resolved) {
					return nil
				}

				blocked := resolved.Unmap()
				if onBlocked != nil {
					onBlocked(blocked)
				}

				return fmt.Errorf("%w: %s", ErrBlocked, blocked)
			},
		}

		return dialer.DialContext(ctx, network, addr)
	}

	return t
}

// proxyConfigured reports whether proxy resolves to a proxy URL for a request to
// registryHost, i.e. whether an HTTP(S) proxy applies to that host (honoring
// NO_PROXY). registryHost may be empty, in which case a representative host is
// used to detect whether any proxy is configured.
func proxyConfigured(proxy func(*http.Request) (*url.URL, error), registryHost string) bool {
	if proxy == nil {
		return false
	}

	if registryHost == "" {
		registryHost = "example.invalid"
	}

	u, err := proxy(&http.Request{URL: &url.URL{Scheme: "https", Host: registryHost}})

	return err == nil && u != nil
}
