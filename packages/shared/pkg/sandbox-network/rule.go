package sandbox_network

import (
	"fmt"
	"strconv"
	"strings"
)

// Rule represents a parsed allow/deny entry with an optional port range.
// Used for both ingress and egress rules.
type Rule struct {
	Host      string // IP, CIDR, or domain
	PortStart uint16 // 0 means all ports
	PortEnd   uint16 // 0 means all ports
	IsDomain  bool
}

// AllPorts returns true if the rule matches all ports.
func (r Rule) AllPorts() bool {
	return r.PortStart == 0 && r.PortEnd == 0
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

	return Rule{
		Host:      host,
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

// splitHostPort splits a rule string into host and port range components.
// Returns portStart=0, portEnd=0 for "all ports".
//
// IPv6 addresses use bracket notation for ports: "[::1]:80", "[::/0]:53-443".
// Without brackets, IPv6 addresses/CIDRs are treated as host-only (all ports).
func splitHostPort(s string) (host string, portStart, portEnd uint16, err error) {
	// Handle bracket notation for IPv6: [host]:port
	if strings.HasPrefix(s, "[") {
		closeBracket := strings.Index(s, "]")
		if closeBracket < 0 {
			return "", 0, 0, fmt.Errorf("missing closing bracket in %q", s)
		}

		host = s[1:closeBracket]
		rest := s[closeBracket+1:]

		if rest == "" {
			return host, 0, 0, nil
		}

		if !strings.HasPrefix(rest, ":") {
			return "", 0, 0, fmt.Errorf("expected ':' after ']' in %q", s)
		}

		portPart := rest[1:]
		if portPart == "" {
			return host, 0, 0, nil
		}

		portStart, portEnd, err = parsePortRange(portPart)
		if err != nil {
			return "", 0, 0, err
		}

		return host, portStart, portEnd, nil
	}

	// Count colons to detect IPv6.
	colonCount := strings.Count(s, ":")
	if colonCount > 1 {
		// Multiple colons → IPv6 address or CIDR, no port separator possible
		// without bracket notation.
		return s, 0, 0, nil
	}

	idx := strings.LastIndex(s, ":")
	if idx < 0 {
		// No colon: bare host, all ports.
		return s, 0, 0, nil
	}

	host = s[:idx]
	portPart := s[idx+1:]

	// Explicit "all ports" — trailing colon with nothing after it.
	if portPart == "" {
		return host, 0, 0, nil
	}

	portStart, portEnd, err = parsePortRange(portPart)
	if err != nil {
		return "", 0, 0, err
	}

	return host, portStart, portEnd, nil
}

// parsePortRange parses a port or port range string.
// "80" → (80, 80, nil), "1-1024" → (1, 1024, nil).
func parsePortRange(s string) (start, end uint16, err error) {
	if startStr, endStr, ok := strings.Cut(s, "-"); ok {
		start, err = parsePort(startStr)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid port range start: %w", err)
		}

		end, err = parsePort(endStr)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid port range end: %w", err)
		}

		if start > end {
			return 0, 0, fmt.Errorf("port range start %d is greater than end %d", start, end)
		}

		return start, end, nil
	}

	port, err := parsePort(s)
	if err != nil {
		return 0, 0, err
	}

	return port, port, nil
}

// parsePort parses and validates a single port number (1-65535).
func parsePort(s string) (uint16, error) {
	n, err := strconv.ParseUint(s, 10, 16)
	if err != nil {
		return 0, fmt.Errorf("invalid port %q: %w", s, err)
	}

	if n == 0 {
		return 0, fmt.Errorf("port must be between 1 and 65535, got 0")
	}

	return uint16(n), nil
}
