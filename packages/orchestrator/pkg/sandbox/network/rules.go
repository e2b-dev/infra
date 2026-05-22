//go:build linux

package network

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/coreos/go-iptables/iptables"
)

// RuleSet buffers iptables rule mutations and applies them to the host
// netfilter tables in a single `iptables-restore --noflush` invocation.
//
// Per-sandbox provisioning previously shelled out to `iptables` ~6 times in
// CreateNetwork (plus ~3 from the egress proxy). On modern Ubuntu where
// `iptables` is `iptables-nft`, every call reads, mutates and rewrites the
// entire table under the xtables lock, so total cost is O(N²) in the number
// of existing rules. Batching collapses that to one fork and one table
// rewrite per CreateNetwork / RemoveNetwork.
type RuleSet struct {
	// keep deterministic ordering per table while still de-duplicating
	// table names.
	tables []string
	rules  map[string][]rule
}

type rule struct {
	op    op
	chain string
	args  []string
}

type op uint8

const (
	opAppend op = iota
	opDelete
)

func (o op) flag() string {
	if o == opAppend {
		return "-A"
	}
	return "-D"
}

// NewRuleSet returns an empty RuleSet ready to accumulate rules.
func NewRuleSet() *RuleSet {
	return &RuleSet{rules: map[string][]rule{}}
}

// Append records an `-A CHAIN args...` line in the given table.
func (r *RuleSet) Append(table, chain string, args ...string) {
	r.addRule(table, rule{op: opAppend, chain: chain, args: args})
}

// Delete records a `-D CHAIN args...` line in the given table.
func (r *RuleSet) Delete(table, chain string, args ...string) {
	r.addRule(table, rule{op: opDelete, chain: chain, args: args})
}

// Empty reports whether the RuleSet has no buffered changes.
func (r *RuleSet) Empty() bool {
	return len(r.tables) == 0
}

// Apply writes the buffered rules to `iptables-restore --noflush` so all
// changes land in one fork + one table rewrite per touched table. `--noflush`
// preserves existing rules; only buffered `-A`/`-D` lines are merged in. The
// operation is per-table atomic: either every line in a table parses and
// applies, or none does.
func (r *RuleSet) Apply(ctx context.Context) error {
	if r.Empty() {
		return nil
	}

	var buf bytes.Buffer
	for _, t := range r.tables {
		fmt.Fprintf(&buf, "*%s\n", t)
		for _, rl := range r.rules[t] {
			fmt.Fprintf(&buf, "%s %s", rl.op.flag(), rl.chain)
			for _, a := range rl.args {
				buf.WriteByte(' ')
				buf.WriteString(a)
			}
			buf.WriteByte('\n')
		}
		buf.WriteString("COMMIT\n")
	}

	cmd := exec.CommandContext(ctx, "iptables-restore", "--noflush", "--wait")
	cmd.Stdin = &buf
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("iptables-restore --noflush: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// ApplyBestEffort tries Apply first; on failure (typically because a delete
// targeted a rule that doesn't exist) it falls back to per-rule shellouts
// and collects rather than aborts on individual errors. Use this from
// teardown paths where partial state from a failed earlier CreateNetwork is
// a normal occurrence.
func (r *RuleSet) ApplyBestEffort(ctx context.Context) error {
	if err := r.Apply(ctx); err == nil {
		return nil
	}

	t, err := iptables.New()
	if err != nil {
		return fmt.Errorf("init iptables fallback: %w", err)
	}

	var errs []error
	for _, table := range r.tables {
		for _, rl := range r.rules[table] {
			args := append([]string{rl.chain}, rl.args...)
			switch rl.op {
			case opAppend:
				if e := t.Append(table, args[0], args[1:]...); e != nil {
					errs = append(errs, fmt.Errorf("iptables append %s/%s: %w", table, rl.chain, e))
				}
			case opDelete:
				if e := t.Delete(table, args[0], args[1:]...); e != nil {
					errs = append(errs, fmt.Errorf("iptables delete %s/%s: %w", table, rl.chain, e))
				}
			}
		}
	}
	return errors.Join(errs...)
}

func (r *RuleSet) addRule(table string, rl rule) {
	if _, ok := r.rules[table]; !ok {
		r.tables = append(r.tables, table)
	}
	r.rules[table] = append(r.rules[table], rl)
}
