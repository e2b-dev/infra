//go:build linux

package userfaultfd

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// These tests drive the CoW window through the REAL serve loop — live
// Serve(), real settleRequests/readSerial locking, a real
// BeginCoWExport/EndCoWExport pairing — where the isolated CoWWindow tests
// use in-memory shims. The parent touches guest memory; the serving child
// installs pages, resolves WP faults through EnsureCopied, and runs the
// REMOVE tripwire on real MADV_DONTNEED events.

// TestServeLoopCoWFaultPathCapture pins the fault-path capture: a guest
// write to an armed window page must have its pre-image copied into the sink
// by the WP resolve BEFORE the write proceeds, and the sweep then completes
// the rest of the set.
func TestServeLoopCoWFaultPathCapture(t *testing.T) {
	t.Parallel()

	cfg := testConfig{
		name:          "cow fault capture",
		pagesize:      header.PageSize,
		numberOfPages: 4,
		// alwaysWP: pages install write-protect-armed; syncWP: the uffd is
		// registered without WP_ASYNC, so the post-install write below
		// BLOCKS on a real WP event the serve loop must resolve — the
		// production use-sync-wp configuration the window requires. (Under
		// WP_ASYNC the kernel auto-resolves the write and the fault path
		// never runs.)
		alwaysWP: true,
		syncWP:   true,
	}

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	h, err := configureCrossProcessTest(ctx, t, cfg)
	require.NoError(t, err)

	// Install every page armed (reads: content == source).
	for i := range int64(cfg.numberOfPages) {
		require.NoError(t, h.executeOperation(ctx, operation{offset: i * int64(cfg.pagesize), mode: operationModeRead}))
	}

	require.NoError(t, h.client.CoWBegin([]uint64{0, 1, 2, 3}))

	// A write to page 0 WP-faults; the resolve must capture the pre-image
	// (source content) into the sink before the write is allowed through.
	require.NoError(t, h.executeOperation(ctx, operation{offset: 0, mode: operationModeWrite}))

	state, err := h.client.CoWState()
	require.NoError(t, err)
	require.False(t, state.Canceled, "no cancel expected: %s", state.CancelCause)
	require.GreaterOrEqual(t, state.Copied, int64(1), "the WP resolve must have captured the written page")
	src := h.data.Content()
	ps := int64(cfg.pagesize)
	assert.Equal(t, src[:ps], state.Sink[:ps], "fault-path capture must hold the pre-write content")

	// The sweep completes the remaining pages and uninstalls the window
	// through the serve loop's locks.
	sweep, err := h.client.CoWSweep()
	require.NoError(t, err)
	require.Empty(t, sweep.SweepError)
	require.Empty(t, sweep.CancelCause)
}

// TestServeLoopRemoveTripwire pins the corruption tripwire end to end: a
// real MADV_DONTNEED against a window page, delivered as a UFFD REMOVE event
// through the live serve loop, must cancel the window — and the sweep must
// report the cancellation rather than success.
func TestServeLoopRemoveTripwire(t *testing.T) {
	t.Parallel()

	cfg := testConfig{
		name:          "cow remove tripwire",
		pagesize:      header.PageSize,
		numberOfPages: 4,
		alwaysWP:      true,
		syncWP:        true,
		removeEnabled: true,
	}

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	h, err := configureCrossProcessTest(ctx, t, cfg)
	require.NoError(t, err)

	for i := range int64(cfg.numberOfPages) {
		require.NoError(t, h.executeOperation(ctx, operation{offset: i * int64(cfg.pagesize), mode: operationModeRead}))
	}

	require.NoError(t, h.client.CoWBegin([]uint64{0, 1, 2, 3}))

	// Zap page 1: the serve loop's REMOVE batch must trip the cancel.
	require.NoError(t, h.executeOperation(ctx, operation{offset: int64(cfg.pagesize), mode: operationModeRemove}))

	// REMOVE processing is asynchronous on the serve loop; poll the window.
	require.Eventually(t, func() bool {
		state, stateErr := h.client.CoWState()

		return stateErr == nil && state.Canceled
	}, 20*time.Second, 50*time.Millisecond, "the REMOVE tripwire must cancel the window")

	state, err := h.client.CoWState()
	require.NoError(t, err)
	assert.Contains(t, state.CancelCause, "REMOVE zapped window pages")

	// The sweep must refuse: acceptance serializes behind the serve loop's
	// read→parse→cancel section, so a canceled window can never sweep to
	// success.
	sweep, err := h.client.CoWSweep()
	require.NoError(t, err)
	assert.Contains(t, sweep.SweepError, "memory CoW window canceled")
	assert.Contains(t, sweep.CancelCause, "REMOVE zapped window pages")
}
