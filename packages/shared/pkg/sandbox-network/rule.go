package sandbox_network

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Rule represents a parsed allow/deny entry with an optional port range.
type Rule struct {
	Host      string     // IP, CIDR, or domain
	IPNet     *net.IPNet // parsed CIDR; nil for domain rules
	PortStart uint16     // 0 means all ports
	PortEnd   uint16     // 0 means all ports
	IsDomain  bool
}

// ContainsIP returns true if the rule's CIDR contains the given IP.
// Always returns false for domain rules.
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

// ParseRule parses a string entry into a Rule.
// Supported formats:
//   - "8.8.8.8"           → IP, all ports
//   - "8.8.8.0/24"        → CIDR, all ports
//   - "8.8.8.8:80"        → IP, port 80
//   - "8.8.8.0/24:1-1024" → CIDR, port range
//   - "8.8.8.8:"          → IP, all ports (explicit)
//   - "example.com"       → domain, all ports
//   - "example.com:443"   → domain, port 443
func ParseRule(s string) (Rule, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Rule{}, fmt.Errorf("empty network rule")
	}

	host, portStart, portEnd, err := splitHostPort(s)
	if err != nil {
		return Rule{}, err
	}

	isDomain := !IsIPOrCIDR(host)

	var ipNet *net.IPNet
	if !isDomain {
		cidr := host
		if !strings.Contains(cidr, "/") {
			// Bare IP: use /32 for IPv4, /128 for IPv6.
			if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
				cidr += "/128"
			} else {
				cidr += "/32"
			}
		}

		_, ipNet, err = net.ParseCIDR(cidr)
		if err != nil {
			return Rule{}, fmt.Errorf("invalid IP/CIDR %q: %w", host, err)
		}
	}

	return Rule{
		Host:      host,
		IPNet:     ipNet,
		PortStart: portStart,
		PortEnd:   portEnd,
		IsDomain:  isDomain,
	}, nil
}

// ParseRules parses a list of string entries into Rules.
func ParseRules(entries []string) ([]Rule, error) {
	rules := make([]Rule, 0, len(entries))
	for _, entry := range entries {
		rule, err := ParseRule(entry)
		if err != nil {
			return nil, fmt.Errorf("invalid entry %q: %w", entry, err)
		}

		rules = append(rules, rule)
	}

	return rules, nil
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

	for i := range a.Allowed {
		if a.Allowed[i].ContainsIP(ip) && a.Allowed[i].PortInRange(port) {
			return true
		}
	}

	for i := range a.Denied {
		if a.Denied[i].ContainsIP(ip) && a.Denied[i].PortInRange(port) {
			return false
		}
	}

	return true
}

// HasRules returns true if any rules are configured.
func (a *ACL) HasRules() bool {
	return a != nil && (len(a.Allowed) > 0 || len(a.Denied) > 0)
}

// splitHostPort splits a rule string into host and optional port range.
// Uses net.SplitHostPort for bracket/IPv6 handling, with fallback for bare hosts.
func splitHostPort(s string) (host string, portStart, portEnd uint16, err error) {
	h, portStr, splitErr := net.SplitHostPort(s)
	if splitErr != nil {
		// Strip brackets for bare "[::1]" (no port).
		if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
			return s[1 : len(s)-1], 0, 0, nil
		}

		// No port part — bare host (IP, CIDR, domain, or unbracketed IPv6).
		return s, 0, 0, nil
	}

	host = h
	if host == "" {
		host = "0.0.0.0/0" // ":443" means all IPs, port 443
	}

	if portStr == "" {
		return host, 0, 0, nil // trailing colon = all ports
	}

	portStart, portEnd, err = parsePortRange(portStr)

	return host, portStart, portEnd, err
}

// parsePortRange parses "80" → (80, 80) or "80-443" → (80, 443).
func parsePortRange(s string) (uint16, uint16, error) {
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

// parsePort validates a single port number (1-65535).
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
