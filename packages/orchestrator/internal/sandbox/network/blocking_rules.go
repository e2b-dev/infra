package network

import (
	"fmt"
	"strings"

	"github.com/coreos/go-iptables/iptables"

	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
)

var blockedRanges = []string{
	"10.0.0.0/8",
	"169.254.0.0/16",
	"192.168.0.0/16",
	"172.16.0.0/12",
}

var logsCollectorIP = strings.Replace(logs.CollectorPublicIP, "http://", "", 1) + "/32"

func getAllowRuleForLogs(slot *Slot) []string {
	return []string{"-p", "all", "-i", slot.TapName(), "-d", logsCollectorIP, "-j", "ACCEPT"}
}

func getBlockingRuleForEverything(slot *Slot) []string {
	return []string{"-p", "all", "-i", slot.TapName(), "-j", "DROP"}
}

func getBlockingRule(slot *Slot, ipRange string) []string {
	return []string{"-p", "all", "-i", slot.TapName(), "-d", ipRange, "-j", "DROP"}
}

func getAllowRuleForEstablished(slot *Slot) []string {
	return []string{"-p", "tcp", "-i", slot.TapName(), "-m", "conntrack", "--ctstate", "ESTABLISHED,RELATED", "-j", "ACCEPT"}
}

func (s *Slot) addBlockingRules(tables *iptables.IPTables) error {
	// TEMPORARY HACK: We disable all traffic by default just for specific customer.
	// This should not be deployed in general production.
	blockAllRule := getBlockingRuleForEverything(s)
	err := tables.Append("filter", "FORWARD", blockAllRule...)
	if err != nil {
		return fmt.Errorf("error adding blocking rule: %w", err)
	}

	for _, ipRange := range blockedRanges {
		rule := getBlockingRule(s, ipRange)

		err := tables.Append("filter", "FORWARD", rule...)
		if err != nil {
			return fmt.Errorf("error adding blocking rule: %w", err)
		}
	}

	allowLogsRule := getAllowRuleForLogs(s)
	err = tables.Insert("filter", "FORWARD", 1, allowLogsRule...)
	if err != nil {
		return fmt.Errorf("error adding allow logs rule: %w", err)
	}

	allowRule := getAllowRuleForEstablished(s)

	err = tables.Insert("filter", "FORWARD", 1, allowRule...)
	if err != nil {
		return fmt.Errorf("error adding response rule: %w", err)
	}

	return nil
}
