package network

import (
	"fmt"
	"github.com/coreos/go-iptables/iptables"
)

var blockedRanges = []string{
	"10.0.0.0/8",
	"169.254.0.0/16",
	"192.168.0.0/16",
	"172.16.0.0/12",
}

func getBlockingRule(slot *Slot, ipRange string) []string {
	return []string{"-p", "all", "-i", slot.TapName(), "-d", ipRange, "-j", "DROP"}
}

func getAllowRule(slot *Slot) []string {
	return []string{"-p", "tcp", "-i", slot.TapName(), "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"}
}

func (s *Slot) addBlockingRules(tables *iptables.IPTables) error {
	for _, ipRange := range blockedRanges {
		rule := getBlockingRule(s, ipRange)

		err := tables.Append("filter", "FORWARD", rule...)
		if err != nil {
			return fmt.Errorf("error adding blocking rule: %w", err)
		}
	}

	allowRule := getAllowRule(s)

	err := tables.Insert("filter", "FORWARD", 1, allowRule...)
	if err != nil {
		return fmt.Errorf("error adding response rule: %w", err)
	}

	return nil
}
