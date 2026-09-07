package envdbin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolverServesAHitFromTheCache(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "envd")
	writeSource(t, src, "binary-v1", time.Unix(1_700_000_000, 0))

	p := &countingProbe{version: "0.7.0"}
	c := newCacheForTest(t, filepath.Join(dir, "cache"), p.probe)
	require.NoError(t, c.Warm(t.Context(), src))
	probesAfterWarm := p.count()

	r := NewResolver(c, OpLive)

	version, err := r.Version(t.Context(), src)
	require.NoError(t, err)
	require.Equal(t, "0.7.0", version)
	require.Equal(t, OutcomeHit, r.Outcome())
	require.Equal(t, probesAfterWarm, p.count(), "a hit must not probe anything")

	entry := r.EntryFor(src)
	require.NotNil(t, entry)
	require.Equal(t, version, entry.Version,
		"the delivered bytes must be the ones whose version was reported")
	localPath, ok := r.SourcePath(t.Context(), src)
	require.True(t, ok)
	require.Equal(t, entry.LocalPath, localPath)
	require.NotEqual(t, src, localPath)
}

func TestResolverRefusesAnEntryForADifferentPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	probed := filepath.Join(dir, "envd")
	other := filepath.Join(dir, "envd.abcdef0")
	writeSource(t, probed, "binary-v1", time.Unix(1_700_000_000, 0))
	writeSource(t, other, "binary-v2", time.Unix(1_700_000_500, 0))

	p := &countingProbe{version: "0.7.0"}
	c := newCacheForTest(t, filepath.Join(dir, "cache"), p.probe)
	require.NoError(t, c.Warm(t.Context(), probed))

	r := NewResolver(c, OpLive)
	_, err := r.Version(t.Context(), probed)
	require.NoError(t, err)

	// Handing back a copy of a binary nobody asked about would deliver bytes whose
	// version was never the one recorded.
	require.Nil(t, r.EntryFor(other))

	// And nor may it hand back the SOURCE. An engaged resolver that reads the mount
	// puts back both costs this package exists to remove -- a mount read on the
	// resume path, and bytes whose version was never verified -- and does it
	// silently, since a caller cannot tell a cached path from a source path by
	// looking at it. Only a nil resolver may return the source.
	otherPath, otherOK := r.SourcePath(t.Context(), other)
	require.False(t, otherOK, "an engaged resolver must refuse rather than read the source")
	require.Empty(t, otherPath)

	// The positive twin: the path it did resolve still points at its copy, so the
	// refusal above is about the missing entry and not a resolver that refuses
	// everything.
	require.NotNil(t, r.EntryFor(probed))
	probedPath, probedOK := r.SourcePath(t.Context(), probed)
	require.True(t, probedOK)
	require.NotEqual(t, probed, probedPath, "the resolved path is served from the copy")
}

// A nil Resolver is what both call sites hold when the feature flag is off, so
// every method must behave as the uncached path does rather than panic.
func TestNilResolverBehavesAsTheUncachedPath(t *testing.T) {
	t.Parallel()

	var r *Resolver

	nilPath, nilOK := r.SourcePath(t.Context(), "/fc-envd/envd")
	require.True(t, nilOK)
	require.Equal(t, "/fc-envd/envd", nilPath)
	require.Nil(t, r.EntryFor("/fc-envd/envd"))
	require.Empty(t, r.Outcome())
}

func TestResolverWithAnUnusableCacheDefersEveryUpgrade(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "envd")
	writeSource(t, src, "binary-v1", time.Unix(1_700_000_000, 0))

	blocker := filepath.Join(dir, "blocker")
	require.NoError(t, os.WriteFile(blocker, []byte("x"), 0o644))

	p := &countingProbe{version: "0.7.0"}
	c := newCacheForTest(t, filepath.Join(blocker, "cache"), p.probe)
	r := NewResolver(c, OpLive)

	// A node whose cache directory is broken never warms, so it never upgrades.
	// This is the cost of keeping the mount off the resume path, and it is the one
	// case where doing so is worse than not caching at all -- which is why the
	// deferral is counted (gated{binary_not_cached}) and the failed warms are too.
	// Resumes themselves are unaffected: this returns a reason, not an error the
	// caller has to handle.
	_, err := r.Version(t.Context(), src)
	require.ErrorIs(t, err, ErrNotCached)
	require.Equal(t, OutcomeMiss, r.Outcome())
	require.NotContains(t, p.paths(), src,
		"a deferral must not probe the source; that is the cost being avoided")
}

// TestResolverDefersRatherThanReadingTheSourceOnAMiss pins the decision this cache
// turns on: the resume path reads no bytes off the mount, so a miss defers the
// upgrade rather than answering from the source.
//
// The probe count is the assertion that matters. Answering a miss from the source
// would be correct and invisible -- the upgrade would succeed, the version would be
// right -- and it would silently reinstate the multi-second cost on a path a
// customer waits on, bounded only by the resume's own deadline.
func TestResolverDefersRatherThanReadingTheSourceOnAMiss(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "envd")
	writeSource(t, src, "binary-v1", time.Unix(1_700_000_000, 0))

	p := &countingProbe{version: "0.7.0"}
	c := newCacheForTest(t, filepath.Join(dir, "cache"), p.probe)
	r := NewResolver(c, OpLive)

	version, err := r.Version(t.Context(), src)
	require.ErrorIs(t, err, ErrNotCached)
	require.Empty(t, version)
	require.Equal(t, OutcomeMiss, r.Outcome())
	// By PATH, not by count: the background warm probes the local copy through this
	// same probe, so a count assertion would race it and would also pass for the
	// wrong reason. What must never happen is a probe of the SOURCE.
	require.NotContains(t, p.paths(), src, "the source must not be probed on a miss")
	require.Nil(t, r.EntryFor(src), "a deferral resolves nothing to deliver")

	// It still fills the cache for the resumes after this one, which is what makes
	// the deferral cost one cycle rather than being permanent.
	require.Eventually(t, func() bool {
		_, ok := c.Lookup(src)

		return ok
	}, 10*time.Second, 5*time.Millisecond, "the miss must warm the cache in the background")

	next := NewResolver(c, OpLive)
	v, err := next.Version(t.Context(), src)
	require.NoError(t, err)
	require.Equal(t, "0.7.0", v)
	require.Equal(t, OutcomeHit, next.Outcome())
}

// TestResolverRefusesWhenTheCopyHasVanished covers the one remaining gap between
// resolving and delivering: the copy can be evicted or superseded in between.
//
// The source is deliberately left untouched, so falling back to it would deliver
// the right bytes. It is still refused: reading it is a 13 MB read off the mount
// on the resume path, which is the cost this design exists to remove, and the next
// resume retries at no cost.
func TestResolverRefusesWhenTheCopyHasVanished(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "envd")
	writeSource(t, src, "binary-v1", time.Unix(1_700_000_000, 0))

	p := &countingProbe{version: "0.7.0"}
	c := newCacheForTest(t, filepath.Join(dir, "cache"), p.probe)
	require.NoError(t, c.Warm(t.Context(), src))

	r := NewResolver(c, OpLive)
	_, err := r.Version(t.Context(), src)
	require.NoError(t, err)
	entry := r.EntryFor(src)
	require.NotNil(t, entry)

	require.NoError(t, os.Remove(entry.LocalPath))

	path, ok := r.SourcePath(t.Context(), src)
	require.False(t, ok, "a vanished copy must be refused, not read from the mount instead")
	require.Empty(t, path)
	require.FileExists(t, src, "the source is intact; it is deliberately not the fallback")
}

// TestGatedReasonSeparatesADeferralFromAFailedProbe pins the label split. Both
// arrive at the call site as the same getversion_failed, and conflating them would
// make a real misconfiguration indistinguishable from the expected state of a node
// that has not warmed — on every freshly booted node, which is where a ramp starts.
func TestGatedReasonSeparatesADeferralFromAFailedProbe(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		reason  string
		outcome Outcome
		want    string
	}{
		{"a deferral is relabelled", "getversion_failed", OutcomeMiss, ReasonNotCached},
		{"a genuine probe failure is not", "getversion_failed", OutcomeHit, "getversion_failed"},
		{"flag off passes through untouched", "getversion_failed", "", "getversion_failed"},
		{"other reasons are never rewritten", "not_staged", OutcomeMiss, "not_staged"},
		{"downgrade is never rewritten", "downgrade", OutcomeMiss, "downgrade"},
		{"an empty reason stays empty", "", OutcomeMiss, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, GatedReason(tc.reason, tc.outcome))
		})
	}
}

// A resume whose own lookup could not stat the source must not answer that with
// a warm. The warm would open by taking the stat that has just failed, and with
// no identity in hand its slot can only be claimed after that -- so a mount that
// has stopped answering its metadata would get one goroutine per resume, each
// parked in a stat, which is the pile-up the slot exists to prevent arriving
// through the one path that could not claim one.
func TestALookupWhoseSourceStatFailedSchedulesInsteadOfWarming(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "envd")
	writeSource(t, src, "binary-v1", time.Unix(1_700_000_000, 0))

	c := newCacheForTest(t, filepath.Join(dir, "cache"), func(context.Context, string) (string, error) {
		return "0.7.0", nil
	})

	// Atomic because the shape this test rules out would take this stat from
	// another goroutine, and a counter that only races when the code is wrong
	// reports as a race rather than as the assertion it belongs to.
	var stats atomic.Int64
	c.statSrc = func(string) (os.FileInfo, error) {
		stats.Add(1)

		return nil, errors.New("mount is not answering")
	}

	before := warmsByResult(t)[warmFailed]
	r := NewResolver(c, OpLive)

	_, err := r.Version(t.Context(), src)
	require.ErrorIs(t, err, ErrNotCached, "an unreadable source is a miss, and the upgrade defers")

	// One stat, the lookup's own, and it stays one: no warm was spawned to take a
	// second, and so none of the resumes behind this one spawns one either.
	require.Never(t, func() bool {
		return stats.Load() > 1
	}, 200*time.Millisecond, 10*time.Millisecond,
		"a failed source stat must not be re-taken by a warm")
	require.Equal(t, int64(1), stats.Load())

	// It is still reported and still scheduled, exactly as the warm that used to
	// do this would have been: the node says why it is not upgrading, and the
	// resumes behind this one stop asking the mount at all.
	require.Eventually(t, func() bool {
		return warmsByResult(t)[warmFailed] >= before+1
	}, 10*time.Second, 10*time.Millisecond, "the failure must be counted")
	require.True(t, c.scheduleSuppressed(src), "the failure must arm the retry schedule")
	require.Equal(t, int64(1), stats.Load(), "and the report must not stat the source either")
}

// Both misses defer this resume, and the reads counter calls both a miss -- but only
// one of them is the cache working as designed on a node that has not warmed, answered
// by a warm. The other is a target that cannot be read, which starts no warm at all
// and recurs until the target comes back, so relabeling it as a deferral would put it
// behind the self-clearing label and leave it unlogged.
func TestOnlyAnUnwarmedCacheIsRelabeledAsADeferral(t *testing.T) {
	t.Parallel()

	t.Run("an unreadable source keeps getversion_failed", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		src := filepath.Join(dir, "envd")
		writeSource(t, src, "binary-v1", time.Unix(1_700_000_000, 0))

		c := newCacheForTest(t, filepath.Join(dir, "cache"), func(context.Context, string) (string, error) {
			return "0.7.0", nil
		})
		c.statSrc = func(string) (os.FileInfo, error) {
			return nil, errors.New("mount is not answering")
		}

		r := NewResolver(c, OpLive)
		_, err := r.Version(t.Context(), src)
		require.ErrorIs(t, err, ErrNotCached)

		assert.Equal(t, OutcomeMiss, r.Outcome(),
			"the read outcome is still a miss: this resume read nothing from the cache")
		assert.Equal(t, "getversion_failed", GatedReason("getversion_failed", r.DeferralOutcome()),
			"an unreadable target must stay an operator error the upgrade logs")
	})

	t.Run("an unwarmed cache becomes binary_not_cached", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		src := filepath.Join(dir, "envd")
		writeSource(t, src, "binary-v1", time.Unix(1_700_000_000, 0))

		c := newCacheForTest(t, filepath.Join(dir, "cache"), func(context.Context, string) (string, error) {
			return "0.7.0", nil
		})

		r := NewResolver(c, OpLive)
		_, err := r.Version(t.Context(), src)
		require.ErrorIs(t, err, ErrNotCached)

		assert.Equal(t, OutcomeMiss, r.Outcome())
		assert.Equal(t, ReasonNotCached, GatedReason("getversion_failed", r.DeferralOutcome()),
			"a readable source that is merely not warmed yet is the deferral this label is for")
	})
}
