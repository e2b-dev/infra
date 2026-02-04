package network

import (
	"fmt"

	"github.com/coreos/go-iptables/iptables"
)

// tcpProxyConfig holds the configuration for TCP proxy iptables rules.
type tcpProxyConfig struct {
	iface     string // interface name (e.g., veth0)
	httpPort  string // port for HTTP traffic (dport 80)
	tlsPort   string // port for TLS traffic (dport 443)
	otherPort string // port for other TCP traffic
}

// tcpProxyConfig returns the TCP proxy configuration for this slot.
func (s *Slot) tcpProxyConfig() tcpProxyConfig {
	return tcpProxyConfig{
		iface:     s.VethName(),
		httpPort:  s.tcpFirewallHTTPPort,
		tlsPort:   s.tcpFirewallTLSPort,
		otherPort: s.tcpFirewallOtherPort,
	}
}

// tcpProxyRule represents an iptables rule for redirecting TCP traffic to the proxy.
type tcpProxyRule struct {
	dstPort   string // destination port to match (empty = all ports)
	proxyPort string // port to redirect to
	desc      string // description for error messages
}

// rules returns the iptables rules for redirecting TCP traffic to the proxy.
// Separate rules prevent protocol detection from blocking server-first protocols like SSH:
// - Port 80 → HTTP proxy (Host header inspection)
// - Port 443 → TLS proxy (SNI inspection)
// - Other ports → CIDR-only proxy (no protocol inspection)
func (c tcpProxyConfig) rules() []tcpProxyRule {
	return []tcpProxyRule{
		{dstPort: "80", proxyPort: c.httpPort, desc: "HTTP"},
		{dstPort: "443", proxyPort: c.tlsPort, desc: "TLS"},
		{dstPort: "", proxyPort: c.otherPort, desc: "other TCP"},
	}
}

// ruleArgs returns the iptables arguments for a TCP proxy redirect rule.
func (c tcpProxyConfig) ruleArgs(rule tcpProxyRule) []string {
	args := []string{"-i", c.iface, "-p", "tcp"}
	if rule.dstPort != "" {
		args = append(args, "--dport", rule.dstPort)
	}
	args = append(args,
		"-j", "REDIRECT", "--to-port", rule.proxyPort,
	)

	return args
}

// append adds the TCP proxy redirect rules to iptables.
func (c tcpProxyConfig) append(tables *iptables.IPTables) error {
	for _, rule := range c.rules() {
		err := tables.Append("nat", "PREROUTING", c.ruleArgs(rule)...)
		if err != nil {
			return fmt.Errorf("error creating redirect rule for %s traffic: %w", rule.desc, err)
		}
	}

	return nil
}

// delete removes the TCP proxy redirect rules from iptables.
func (c tcpProxyConfig) delete(tables *iptables.IPTables) []error {
	var errs []error
	for _, rule := range c.rules() {
		err := tables.Delete("nat", "PREROUTING", c.ruleArgs(rule)...)
		if err != nil {
			errs = append(errs, fmt.Errorf("error deleting %s egress proxy redirect rule: %w", rule.desc, err))
		}
	}

	return errs
}
