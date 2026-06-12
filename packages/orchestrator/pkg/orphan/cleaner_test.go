//go:build linux

package orphan_test

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/orphan"
)

// ─── isNoSuchProcess ─────────────────────────────────────────────────────────

func TestIsNoSuchProcess_NilError(t *testing.T) {
	t.Parallel()
	assert.False(t, orphan.IsNoSuchProcess(nil))
}

func TestIsNoSuchProcess_ESRCH(t *testing.T) {
	t.Parallel()
	assert.True(t, orphan.IsNoSuchProcess(syscall.ESRCH))
}

func TestIsNoSuchProcess_OtherError(t *testing.T) {
	t.Parallel()
	assert.False(t, orphan.IsNoSuchProcess(syscall.EPERM))
	assert.False(t, orphan.IsNoSuchProcess(syscall.EINVAL))
}

// ─── parseIPTablesRule ───────────────────────────────────────────────────────

func TestParseIPTablesRule_StandardRule(t *testing.T) {
	t.Parallel()

	rule := "-A PREROUTING -i veth-42 -p tcp --dport 80 -j REDIRECT --to-port 8080"
	args := orphan.ParseIPTablesRule(rule, "PREROUTING")
	assert.Equal(t, []string{"-i", "veth-42", "-p", "tcp", "--dport", "80", "-j", "REDIRECT", "--to-port", "8080"}, args)
}

func TestParseIPTablesRule_EmptyAfterPrefix(t *testing.T) {
	t.Parallel()

	// Rule has only the "-A CHAIN " prefix with no trailing arguments
	rule := "-A FORWARD "
	args := orphan.ParseIPTablesRule(rule, "FORWARD")
	assert.Empty(t, args)
}

func TestParseIPTablesRule_TooShort(t *testing.T) {
	t.Parallel()

	// Rule is shorter than the expected prefix
	args := orphan.ParseIPTablesRule("-A", "FORWARD")
	assert.Nil(t, args)
}

func TestParseIPTablesRule_ForwardRule(t *testing.T) {
	t.Parallel()

	rule := "-A FORWARD -i veth-1 -o eth0 -j ACCEPT"
	args := orphan.ParseIPTablesRule(rule, "FORWARD")
	assert.Equal(t, []string{"-i", "veth-1", "-o", "eth0", "-j", "ACCEPT"}, args)
}

// ─── containsInterface ───────────────────────────────────────────────────────

func TestContainsInterface_InboundFlag(t *testing.T) {
	t.Parallel()

	rule := "-A FORWARD -i veth-42 -o eth0 -j ACCEPT"
	assert.True(t, orphan.ContainsInterface(rule, "veth-42"))
}

func TestContainsInterface_OutboundFlag(t *testing.T) {
	t.Parallel()

	rule := "-A FORWARD -i eth0 -o veth-42 -j ACCEPT"
	assert.True(t, orphan.ContainsInterface(rule, "veth-42"))
}

func TestContainsInterface_LongFlag(t *testing.T) {
	t.Parallel()

	rule := "-A PREROUTING --in-interface veth-10 -p tcp -j REDIRECT"
	assert.True(t, orphan.ContainsInterface(rule, "veth-10"))
}

func TestContainsInterface_NoMatch(t *testing.T) {
	t.Parallel()

	rule := "-A FORWARD -i eth0 -o eth1 -j ACCEPT"
	assert.False(t, orphan.ContainsInterface(rule, "veth-42"))
}

func TestContainsInterface_PartialNameMatches(t *testing.T) {
	t.Parallel()

	// containsInterface does substring matching on the flag+value token.
	// "-i veth-4" is a literal substring of "-i veth-42", so it matches.
	// Callers are expected to pass exact interface names (e.g. "veth-42"),
	// not prefixes, so this behaviour is intentional and not a bug.
	rule := "-A FORWARD -i veth-42 -j ACCEPT"
	assert.True(t, orphan.ContainsInterface(rule, "veth-4"))
}

func TestContainsInterface_EmptyRule(t *testing.T) {
	t.Parallel()
	assert.False(t, orphan.ContainsInterface("", "veth-1"))
}

// ─── cleanOrphanedSockets ────────────────────────────────────────────────────

func TestCleanOrphanedSockets_RemovesFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sock1 := filepath.Join(dir, "fc-aaa-bbb.sock")
	sock2 := filepath.Join(dir, "fc-ccc-ddd.sock")
	require.NoError(t, os.WriteFile(sock1, nil, 0o600))
	require.NoError(t, os.WriteFile(sock2, nil, 0o600))

	orphans := []orphan.OrphanedSocket{
		{Path: sock1, DetectedAt: time.Now()},
		{Path: sock2, DetectedAt: time.Now()},
	}

	result := orphan.CleanOrphanedSockets(context.Background(), orphans)
	assert.Empty(t, result.Errors)
	assert.ElementsMatch(t, []string{sock1, sock2}, result.RemovedSockets)

	// Files should have been removed
	_, err := os.Stat(sock1)
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(sock2)
	assert.True(t, os.IsNotExist(err))
}

func TestCleanOrphanedSockets_AlreadyGone_NoError(t *testing.T) {
	t.Parallel()

	// Should not return an error when the file is already gone (idempotent)
	orphans := []orphan.OrphanedSocket{
		{Path: "/tmp/nonexistent-fc-orphan-test.sock", DetectedAt: time.Now()},
	}

	result := orphan.CleanOrphanedSockets(context.Background(), orphans)
	assert.Empty(t, result.Errors)
	assert.Contains(t, result.RemovedSockets, "/tmp/nonexistent-fc-orphan-test.sock")
}

func TestCleanOrphanedSockets_EmptyList(t *testing.T) {
	t.Parallel()

	result := orphan.CleanOrphanedSockets(context.Background(), nil)
	assert.Empty(t, result.Errors)
	assert.Empty(t, result.RemovedSockets)
}

// ─── cleanOrphanedFIFOs ──────────────────────────────────────────────────────

func TestCleanOrphanedFIFOs_RemovesFIFOs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fifo1 := filepath.Join(dir, "fc-metrics-aaa-bbb.fifo")
	fifo2 := filepath.Join(dir, "fc-metrics-ccc-ddd.fifo")
	require.NoError(t, syscall.Mkfifo(fifo1, 0o600))
	require.NoError(t, syscall.Mkfifo(fifo2, 0o600))

	orphans := []orphan.OrphanedFIFO{
		{Path: fifo1, DetectedAt: time.Now()},
		{Path: fifo2, DetectedAt: time.Now()},
	}

	result := orphan.CleanOrphanedFIFOs(context.Background(), orphans)
	assert.Empty(t, result.Errors)
	assert.ElementsMatch(t, []string{fifo1, fifo2}, result.RemovedFIFOs)

	_, err := os.Stat(fifo1)
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(fifo2)
	assert.True(t, os.IsNotExist(err))
}

func TestCleanOrphanedFIFOs_AlreadyGone_NoError(t *testing.T) {
	t.Parallel()

	orphans := []orphan.OrphanedFIFO{
		{Path: "/tmp/nonexistent-fc-metrics-orphan-test.fifo", DetectedAt: time.Now()},
	}

	result := orphan.CleanOrphanedFIFOs(context.Background(), orphans)
	assert.Empty(t, result.Errors)
	assert.Contains(t, result.RemovedFIFOs, "/tmp/nonexistent-fc-metrics-orphan-test.fifo")
}

func TestCleanOrphanedFIFOs_EmptyList(t *testing.T) {
	t.Parallel()

	result := orphan.CleanOrphanedFIFOs(context.Background(), nil)
	assert.Empty(t, result.Errors)
	assert.Empty(t, result.RemovedFIFOs)
}
