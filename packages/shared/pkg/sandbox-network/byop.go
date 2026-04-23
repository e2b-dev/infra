package sandbox_network

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

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
// Rules:
//   - Address must parse as "host:port" with a non-zero port.
//   - Host must be an IP literal or resolve via the system resolver.
//   - Every resolved A/AAAA record must NOT be in DeniedSandboxCIDRs.
//   - If Username == "" then Password must also be "" (no orphan password).
//   - Username and Password may contain {{sandboxID}}; the placeholder is
//     left unsubstituted at validation time.
//
// Passing cfg == nil is valid and returns nil, nil (BYOP not configured).
func ValidateEgressProxy(cfg *EgressProxyConfig) (*EgressProxyConfig, error) {
	if cfg == nil {
		return nil, nil
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
	ips, err := resolveHost(host)
	if err != nil {
		return nil, fmt.Errorf("resolve egress proxy host %q: %w", host, err)
	}
	for _, ip := range ips {
		if IsIPInDeniedSandboxCIDRs(ip) {
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

// RedactEgressProxy returns a copy of cfg safe for logging or inclusion in API
// responses: Password is blanked. Address and Username are preserved. Returns
// nil when cfg is nil.
func RedactEgressProxy(cfg *EgressProxyConfig) *EgressProxyConfig {
	if cfg == nil {
		return nil
	}
	c := *cfg
	c.Password = ""
	return &c
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

// resolveHost returns every IP the host resolves to. For IP literals the
// function returns a single-element slice without touching DNS.
//
// Extracted as a seam for tests which substitute a deterministic resolver.
var resolveHost = func(host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no A/AAAA records for %q", host)
	}
	return ips, nil
}
