package sandbox_network

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Rule represents a pre-parsed network rule with an optional port range.
type Rule struct {
	Host      string
	IPNet     *net.IPNet
	PortStart uint16 // 0 means all ports
	PortEnd   uint16 // 0 means all ports
	IsDomain  bool
}

// ContainsIP returns true if the rule's CIDR contains the given IP.
func (r Rule) ContainsIP(ip net.IP) bool {
	return r.IPNet != nil && r.IPNet.Contains(ip)
}

// AllPorts returns true if the rule matches all ports.
func (r Rule) AllPorts() bool {
	return r.PortStart == 0 && r.PortEnd == 0
}

// HasPort returns true if the rule specifies a port or port range.
func (r Rule) HasPort() bool {
	return !r.AllPorts()
}

// PortInRange returns true if the given port falls within the rule's port range,
// or if the rule matches all ports.
func (r Rule) PortInRange(port uint16) bool {
	return r.AllPorts() || (port >= r.PortStart && port <= r.PortEnd)
}

// ACL holds pre-parsed network access control rules.
// Computed once at config set time to avoid per-connection parsing.
type ACL struct {
	Allowed []Rule
	Denied  []Rule
}

// IsAllowed checks if an IP + port combination is allowed by the ACL.
// Priority: allow wins → deny → default allow.
// Returns true when the ACL is nil (no rules).
func (a *ACL) IsAllowed(ip net.IP, port uint16) bool {
	if a == nil {
		return true
	}

	for _, rule := range a.Allowed {
		if rule.ContainsIP(ip) && rule.PortInRange(port) {
			return true
		}
	}

	for _, rule := range a.Denied {
		if rule.ContainsIP(ip) && rule.PortInRange(port) {
			return false
		}
	}

	return true
}

// HasRules returns true if any rules are configured.
func (a *ACL) HasRules() bool {
	return a != nil && (len(a.Allowed) > 0 || len(a.Denied) > 0)
}

// SplitHostPort splits a network rule string into host and port parts.
// Uses net.SplitHostPort for bracket/IPv6 handling, with fallback for bare hosts.
// Returns empty port string when no port is specified.
func SplitHostPort(s string) (host, port string, err error) {
	h, p, splitErr := net.SplitHostPort(s)
	if splitErr != nil {
		// Strip brackets for bare "[::1]" (no port).
		if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
			return s[1 : len(s)-1], "", nil
		}

		return s, "", nil
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
