package sandbox_network

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// DevAllowedProxyEndpointCIDRs is a dev-only allowlist that overrides
// DeniedSandboxCIDRs at BYOP validation time. Empty in production. Populated
// from BYOP_DEV_ALLOWED_PROXY_CIDRS (comma-separated CIDRs) so a developer
// can point a sandbox at a SOCKS5 proxy on 127.0.0.1 / docker bridge IPs
// without weakening the deny-list itself.
//
// Malformed entries are silently dropped at parse time. Production envs do
// not set the variable; the allowlist is empty and behavior is unchanged.
var DevAllowedProxyEndpointCIDRs = parseCIDRsEnv("BYOP_DEV_ALLOWED_PROXY_CIDRS")

func parseCIDRsEnv(name string) []string {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, _, err := net.ParseCIDR(p); err != nil {
			continue
		}
		out = append(out, p)
	}

	return out
}

// IsIPDevAllowedAsProxyEndpoint reports whether ip falls into any CIDR in
// DevAllowedProxyEndpointCIDRs. Exported so the orchestrator dial-time check
// can apply the same override.
func IsIPDevAllowedAsProxyEndpoint(ip net.IP) bool {
	for _, cidr := range DevAllowedProxyEndpointCIDRs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if ipNet.Contains(ip) {
			return true
		}
	}

	return false
}

// EgressProxyConfig is a transport-agnostic view of a BYOP SOCKS5 proxy
// configuration. The fields mirror the EgressProxy{Address,Username,Password}
// columns on packages/db/pkg/types.SandboxNetworkEgressConfig so callers can
// validate without importing db. Callers copy between the two.
type EgressProxyConfig struct {
	Address  string
	Username string
	Password string
}

// SandboxIDPlaceholder is substituted with the sandbox ID at dial time in
// Username and Password. Validation-time code must leave it literal.
const SandboxIDPlaceholder = "{{sandboxID}}"

// ErrEgressProxyInternalEndpoint is returned when a configured BYOP endpoint
// resolves to an IP that would bridge the sandbox back into E2B infrastructure
// (DeniedSandboxCIDRs: 10/8, 127/8, 169.254/16, 172.16/12, 192.168/16, ::1,
// fc00::/7, fe80::/10). Kept as a sentinel so callers can distinguish this
// class of rejection from malformed input.
var ErrEgressProxyInternalEndpoint = errors.New("egress proxy endpoint resolves to an internal / denied IP range")

// HostResolver resolves a hostname to one or more IPs. For IP literals an
// implementation should return the literal without touching DNS. Implementations
// must honor ctx (cancellation/deadline). Injected into ValidateEgressProxy so
// tests can substitute a deterministic table.
type HostResolver func(ctx context.Context, host string) ([]net.IP, error)

// DefaultHostResolver uses net.DefaultResolver.LookupIPAddr and honors the
// caller's context. IP literals are returned without a lookup.
func DefaultHostResolver(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("no A/AAAA records for %q", host)
	}
	ips := make([]net.IP, len(addrs))
	for i, a := range addrs {
		ips[i] = a.IP
	}

	return ips, nil
}

// ValidateEgressProxy checks a BYOP SOCKS5 config and rejects configurations
// that would expose E2B infrastructure or are malformed. On success it returns
// a canonical copy (host lower-cased, whitespace trimmed); the input is not
// mutated.
//
// The validator is intentionally orchestrator-agnostic: it does NOT check the
// per-orchestrator OrchestratorInSandboxIPAddress. That check happens at dial
// time in the egress proxy, which also re-resolves the hostname to guard
// against DNS-rebind attacks.
//
// resolve is the resolver used to map host to IPs. Pass DefaultHostResolver
// in production; tests pass a stub. If nil, DefaultHostResolver is used.
//
// Rules:
//   - Address must parse as "host:port" with a non-zero port.
//   - Host must be an IP literal or resolve via the provided resolver.
//   - Every resolved A/AAAA record must NOT be in DeniedSandboxCIDRs.
//   - If Username == "" then Password must also be "" (no orphan password).
//   - Username and Password may contain {{sandboxID}}; the placeholder is
//     left unsubstituted at validation time.
//
// Passing cfg == nil is valid and returns nil, nil (BYOP not configured).
func ValidateEgressProxy(ctx context.Context, cfg *EgressProxyConfig, resolve HostResolver) (*EgressProxyConfig, error) {
	if cfg == nil {
		return nil, nil
	}
	if resolve == nil {
		resolve = DefaultHostResolver
	}

	address := strings.TrimSpace(cfg.Address)
	if address == "" {
		return nil, errors.New("egress proxy address must not be empty")
	}

	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("egress proxy address must be in host:port form: %w", err)
	}
	host = strings.TrimSpace(strings.ToLower(host))
	if host == "" {
		return nil, errors.New("egress proxy host must not be empty")
	}
	portNum, err := strconv.Atoi(port)
	if err != nil || portNum <= 0 || portNum > 65535 {
		return nil, fmt.Errorf("egress proxy port %q is not a valid 1-65535 value", port)
	}

	// Resolve and ensure no resolved IP falls in the denied set.
	ips, err := resolve(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve egress proxy host %q: %w", host, err)
	}
	for _, ip := range ips {
		if IsIPInDeniedSandboxCIDRs(ip) && !IsIPDevAllowedAsProxyEndpoint(ip) {
			return nil, fmt.Errorf("%w: %s resolves to %s", ErrEgressProxyInternalEndpoint, host, ip)
		}
	}

	username := cfg.Username
	password := cfg.Password
	if username == "" && password != "" {
		return nil, errors.New("egress proxy password must be empty when username is empty")
	}

	return &EgressProxyConfig{
		Address:  net.JoinHostPort(host, strconv.Itoa(portNum)),
		Username: username,
		Password: password,
	}, nil
}

// IsIPInDeniedSandboxCIDRs reports whether ip falls into any entry of
// DeniedSandboxCIDRs. Used by both API-time validation and the orchestrator's
// dial-time check so a single denylist drives both layers.
func IsIPInDeniedSandboxCIDRs(ip net.IP) bool {
	for _, cidr := range DeniedSandboxCIDRs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if ipNet.Contains(ip) {
			return true
		}
	}

	return false
}
