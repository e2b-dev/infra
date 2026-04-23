package tcpfirewall

// BYOP SOCKS5 dialer — the per-sandbox egress tunnel hook.
//
// When sbx.Config.NetworkEgress.EgressProxyAddress is non-empty, this file
// provides a dialContextFunc that routes the connection through the user's
// SOCKS5 proxy instead of dialing the destination directly. It is wired into
// tcpproxy.DialProxy in handlers.go.
//
// Security invariants (see plans/byop-socks5.md §3):
//
//   - ValidateEgressProxy rejected denied endpoints at API time.
//     newSOCKS5DialContext re-resolves the proxy host on every dial and
//     rejects any resolved IP in DeniedSandboxCIDRs (DNS-rebind guard).
//   - Credentials: Username / Password support the {{sandboxID}} placeholder,
//     substituted per-sandbox at dial time so users can do per-sandbox
//     accounting or routing on their own SOCKS5 server without minting
//     credentials per sandbox.
//   - Password is never logged; redact-before-log is the caller's contract.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	xproxy "golang.org/x/net/proxy"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	sandbox_network "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-network"
)

// sandboxIDPlaceholder is the literal substituted with the sandbox ID in
// Username/Password at dial time. Shared constant with the shared validator
// (sandbox_network.SandboxIDPlaceholder) — kept as a local alias so tests
// that only depend on tcpfirewall don't have to pull the shared package.
const sandboxIDPlaceholder = sandbox_network.SandboxIDPlaceholder

// dialContextFunc matches the signature of net.Dialer.DialContext and
// tcpproxy.DialProxy.DialContext.
type dialContextFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// socks5AuthFromEgress builds SOCKS5 Auth (RFC 1929) from the egress config.
// Returns nil when both fields are empty (SOCKS5 no-auth). The
// {{sandboxID}} placeholder is expanded with the sandbox ID so per-sandbox
// identity can be asserted to the upstream SOCKS5 server.
func socks5AuthFromEgress(egress *orchestrator.SandboxNetworkEgressConfig, sandboxID string) *xproxy.Auth {
	user := egress.GetEgressProxyUsername()
	pass := egress.GetEgressProxyPassword()
	if user == "" && pass == "" {
		return nil
	}
	return &xproxy.Auth{
		User:     strings.ReplaceAll(user, sandboxIDPlaceholder, sandboxID),
		Password: strings.ReplaceAll(pass, sandboxIDPlaceholder, sandboxID),
	}
}

// socks5DialContextFromEgress returns a dialContextFunc that tunnels through
// the sandbox's BYOP SOCKS5 proxy, or nil when BYOP is not configured. The
// nil return is the zero-overhead fast path — callers check for nil before
// installing the override.
func socks5DialContextFromEgress(sbx *sandbox.Sandbox) dialContextFunc {
	egress := sbx.Config.GetNetworkEgress()
	addr := egress.GetEgressProxyAddress()
	if addr == "" {
		return nil
	}
	auth := socks5AuthFromEgress(egress, sbx.Runtime.SandboxID)
	return newSOCKS5DialContext(addr, auth)
}

// ErrSOCKS5EndpointInternal is returned by the SOCKS5 dialer when the
// proxy host re-resolves to an IP in DeniedSandboxCIDRs at dial time.
// Distinguished so metrics can count DNS-rebind rejections separately from
// transport-level dial errors.
var ErrSOCKS5EndpointInternal = errors.New("socks5 proxy host resolves to an internal/denied IP range")

// newSOCKS5DialContext constructs a dialContextFunc that dials through the
// given SOCKS5 proxy. Every invocation re-resolves the proxy host and
// rejects any resolved IP in DeniedSandboxCIDRs — this is the DNS-rebind
// guard (plans/byop-socks5.md §3 invariant 3): the API-time validation may
// have been performed against a different DNS answer.
//
// The returned function matches net.Dialer.DialContext and tcpproxy's
// DialProxy.DialContext exactly.
func newSOCKS5DialContext(proxyAddr string, auth *xproxy.Auth) dialContextFunc {
	baseDialer := &net.Dialer{Timeout: upstreamDialTimeout}

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if err := validateSOCKS5Endpoint(ctx, proxyAddr); err != nil {
			return nil, err
		}

		d, err := xproxy.SOCKS5("tcp", proxyAddr, auth, baseDialer)
		if err != nil {
			return nil, fmt.Errorf("build socks5 dialer: %w", err)
		}
		// xproxy.SOCKS5 returns a xproxy.Dialer; the concrete type also
		// implements xproxy.ContextDialer in modern x/net versions. Fall
		// through to .Dial for older runtimes as a belt-and-suspenders.
		if cd, ok := d.(xproxy.ContextDialer); ok {
			return cd.DialContext(ctx, network, addr)
		}
		return d.Dial(network, addr)
	}
}

// resolveSOCKS5Host is the resolver seam used by validateSOCKS5Endpoint.
// Tests may replace it to drive the DNS-rebind path deterministically.
var resolveSOCKS5Host = func(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		ips = append(ips, a.IP)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no A/AAAA records for %q", host)
	}
	return ips, nil
}

// validateSOCKS5Endpoint re-resolves proxyAddr's host and rejects it if any
// resolved IP falls into DeniedSandboxCIDRs. Returns ErrSOCKS5EndpointInternal
// (wrapped) on rebind rejection.
func validateSOCKS5Endpoint(ctx context.Context, proxyAddr string) error {
	host, _, err := net.SplitHostPort(proxyAddr)
	if err != nil {
		return fmt.Errorf("parse socks5 proxy addr %q: %w", proxyAddr, err)
	}
	ips, err := resolveSOCKS5Host(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve socks5 proxy host %q: %w", host, err)
	}
	for _, ip := range ips {
		if sandbox_network.IsIPInDeniedSandboxCIDRs(ip) {
			return fmt.Errorf("%w: %s -> %s", ErrSOCKS5EndpointInternal, host, ip)
		}
	}
	return nil
}
