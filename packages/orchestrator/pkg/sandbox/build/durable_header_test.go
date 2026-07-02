//go:build linux

package build

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// DurableHeader must return the live header when no durable future is set, and
// otherwise wait for and return the durable (deduped) future's value — this is
// what keeps a Pause from parenting a snapshot off a provisional header.
func TestFile_DurableHeaderPrefersDurableFuture(t *testing.T) {
	t.Parallel()

	live := &header.Header{}
	var f File
	f.header.Store(live)

	// No durable future: returns the live header immediately.
	got, err := f.DurableHeader(t.Context())
	require.NoError(t, err)
	require.Same(t, live, got)

	// With a durable future: blocks until it resolves, then returns it (not live).
	durable := utils.NewSetOnce[*header.Header]()
	f.SetDurableHeader(durable)

	type res struct {
		h   *header.Header
		err error
	}
	ch := make(chan res, 1)
	go func() {
		h, e := f.DurableHeader(t.Context())
		ch <- res{h, e}
	}()

	select {
	case <-ch:
		t.Fatal("DurableHeader returned before the durable future resolved")
	case <-time.After(50 * time.Millisecond):
	}

	deduped := &header.Header{}
	require.NoError(t, durable.SetValue(deduped))

	r := <-ch
	require.NoError(t, r.err)
	require.Same(t, deduped, r.h)
	require.NotSame(t, live, r.h)
}

// SwapHeaderIfCurrent must replace the header only when it still matches, so a
// late provisional→deduped swap cannot clobber a header another writer (e.g. an
// upload finalizing the build) has already installed.
func TestFile_SwapHeaderIfCurrent(t *testing.T) {
	t.Parallel()

	provisional := &header.Header{}
	var f File
	f.header.Store(provisional)

	deduped := &header.Header{}
	finalized := &header.Header{}

	// Current still matches → swaps.
	require.True(t, f.SwapHeaderIfCurrent(provisional, deduped))
	require.Same(t, deduped, f.Header())

	// Current already advanced (upload finalized it) → no-op, newer header kept.
	f.header.Store(finalized)
	require.False(t, f.SwapHeaderIfCurrent(provisional, deduped))
	require.Same(t, finalized, f.Header())
}

// DurableHeaderNow is the non-blocking form: (Header, true) when the durable
// header is known (no swap pending, or the deduped future already resolved) and
// (nil, false) while a swap is still pending — used on latency-sensitive paths.
func TestFile_DurableHeaderNow(t *testing.T) {
	t.Parallel()

	live := &header.Header{}
	var f File
	f.header.Store(live)

	// No durable future: known immediately, returns the live header.
	h, ok := f.DurableHeaderNow()
	require.True(t, ok)
	require.Same(t, live, h)

	// Durable future set but unresolved: not known yet, non-blocking (nil, false).
	durable := utils.NewSetOnce[*header.Header]()
	f.SetDurableHeader(durable)
	h, ok = f.DurableHeaderNow()
	require.False(t, ok)
	require.Nil(t, h)

	// Once resolved: returns the durable (deduped) header.
	deduped := &header.Header{}
	require.NoError(t, durable.SetValue(deduped))
	h, ok = f.DurableHeaderNow()
	require.True(t, ok)
	require.Same(t, deduped, h)
}
