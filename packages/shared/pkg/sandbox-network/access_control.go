package sandbox_network

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

type Egress struct {
	Allowed                Rules
	Denied                 Rules
	AllowedHTTPHostDomains []string
}

type Ingress struct {
	Allowed            Rules
	Denied             Rules
	TrafficAccessToken string
	MaskRequestHost    string
}

type Rules []Rule

func (r Rules) CIDRs() []string {
	if len(r) == 0 {
		return nil
	}
	out := make([]string, 0, len(r))
	for _, rule := range r {
		if rule.IPNet != nil {
			out = append(out, rule.IPNet.String())
		}
	}

	return out
}

type Rule struct {
	IPNet     *net.IPNet
	PortStart uint16 // 0 means all ports
	PortEnd   uint16 // 0 means all ports
}

func (r Rule) ContainsIP(ip net.IP) bool {
	return r.IPNet != nil && r.IPNet.Contains(ip)
}

func (r Rule) PortInRange(port uint16) bool {
	if r.PortStart == 0 && r.PortEnd == 0 {
		return true
	}

	return port >= r.PortStart && port <= r.PortEnd
}

func (e Egress) NoFirewallRules() bool {
	return len(e.Allowed) == 0 && len(e.Denied) == 0
}

func (e Egress) NoHTTPHostDomainRules() bool {
	return len(e.AllowedHTTPHostDomains) == 0
}

func (e Egress) MatchDomain(hostname string) bool {
	for _, pattern := range e.AllowedHTTPHostDomains {
		switch {
		case pattern == "":
			continue
		case strings.EqualFold(pattern, hostname):
			return true
		case strings.EqualFold(pattern, "*"):
			return true
		case strings.HasPrefix(pattern, "*."):
			suffix := pattern[1:]
			if strings.HasSuffix(strings.ToLower(hostname), strings.ToLower(suffix)) {
				return true
			}
		}
	}

	return false
}

// IsAllowed checks if an IP + port combination is allowed by ingress rules.
// Priority: allow wins -> deny -> default allow.
func (i Ingress) IsAllowed(ip net.IP, port uint16) bool {
	for _, rule := range i.Allowed {
		if rule.ContainsIP(ip) && rule.PortInRange(port) {
			return true
		}
	}

	for _, rule := range i.Denied {
		if rule.ContainsIP(ip) && rule.PortInRange(port) {
			return false
		}
	}

	return true
}

func (i Ingress) HasFilters() bool {
	return len(i.Allowed) > 0 || len(i.Denied) > 0
}

// ParseValidRules converts CIDR[:port[-port]] strings into Rules.
// Invalid entries are silently skipped.
func ParseValidRules(entries []string) Rules {
	out := make(Rules, 0, len(entries))
	for _, entry := range entries {
		host, portStr, _ := SplitHostPort(entry)

		_, ipNet, err := net.ParseCIDR(AddressStringToCIDR(host))
		if err != nil {
			continue
		}

		r := Rule{IPNet: ipNet}
		if portStr != "" {
			lo, hi, err := ParsePortRange(portStr)
			if err != nil {
				continue
			}

			r.PortStart, r.PortEnd = lo, hi
		}

		out = append(out, r)
	}

	return out
}

// SplitHostPort splits a network rule string into host and port parts.
// Uses net.SplitHostPort with fallback for bare hosts.
// Returns empty port string when no port is specified.
func SplitHostPort(s string) (string, string, error) {
	h, p, err := net.SplitHostPort(s)
	if err != nil {
		return s, "", nil //nolint:nilerr // fallback: bare host without port is valid
	}

	if h == "" {
		h = "0.0.0.0/0" // ":443" means all IPs, port 443
	}

	return h, p, nil
}

// ParsePortRange validates a port or port range string.
// "80" → (80, 80, nil), "80-443" → (80, 443, nil).
func ParsePortRange(s string) (uint16, uint16, error) {
	lo, hi, isRange := strings.Cut(s, "-")

	start, err := parsePort(lo)
	if err != nil {
		return 0, 0, err
	}

	if !isRange {
		return start, start, nil
	}

	end, err := parsePort(hi)
	if err != nil {
		return 0, 0, err
	}

	if start > end {
		return 0, 0, fmt.Errorf("port range %d-%d: start > end", start, end)
	}

	return start, end, nil
}

func parsePort(s string) (uint16, error) {
	n, err := strconv.ParseUint(s, 10, 16)
	if err != nil {
		return 0, fmt.Errorf("invalid port %q: %w", s, err)
	}

	if n == 0 {
		return 0, fmt.Errorf("port 0 is not valid")
	}

	return uint16(n), nil
}
