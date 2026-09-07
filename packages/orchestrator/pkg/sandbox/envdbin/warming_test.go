package envdbin

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A warm of a wedged source never returns: a read blocked in the kernel is not
// interruptible from userspace, so its warming slot is never released. Keyed on
// the path, that one stuck copy would hold the path for the life of the process
// and no promotion of it could ever warm -- the node would sit on
// binary_not_cached for both upgrade paths with nothing but a flat counter to say
// why. Keyed on the identity, a promotion is a different key and proceeds, which
// is the recovery that matters: a stuck warm blocks nothing whose absence hurts
// until there are new bytes to fetch.
func TestAWarmStuckOnOneIdentityDoesNotHoldOutTheNext(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "envd")
	writeGeneration(t, src, 1)

	// The first generation's probe never returns, which is how a warm holds its
	// slot open for the whole test. Later generations probe normally.
	wedged := make(chan struct{})
	probe := func(ctx context.Context, path string) (string, error) {
		v, err := contentProbe(ctx, path)
		if err != nil {
			return "", err
		}
		if v == "v1" {
			<-wedged

			return "", context.Canceled
		}

		return v, nil
	}

	c := newCacheForTest(t, filepath.Join(dir, "cache"), probe)
	// Registered after the cache, so it runs BEFORE the cache's drain: cleanups
	// are last-in-first-out, and the drain waits for this warm.
	t.Cleanup(func() { close(wedged) })

	gen1 := mustStat(t, src)
	c.warmAsyncFor(t.Context(), src, gen1)

	// Precondition: the stuck warm holds its slot. Without this the test would
	// pass by warming a path nothing was holding.
	require.Eventually(t, func() bool {
		ok, _ := c.beginWarm(src, gen1)
		if ok {
			c.endWarm(src, gen1)
		}

		return !ok
	}, 10*time.Second, 10*time.Millisecond,
		"the first warm never claimed its slot, so nothing is being held out")

	// The promotion. Its bytes are a different identity, so the slot the stuck
	// warm holds is not its slot.
	writeGeneration(t, src, 2)
	c.warmAsyncFor(t.Context(), src, mustStat(t, src))

	require.Eventually(t, func() bool {
		e, ok := c.Lookup(src)

		return ok && e.Version == "v2"
	}, 30*time.Second, 10*time.Millisecond,
		"a promotion must warm while an earlier generation's warm is still wedged")
}

// The refusal is the only symptom a wedged source has. The warm that would have
// counted and logged the failure is the warm that never ran, so without counting
// the refusal the node reports reads{miss} on every resume with warms flat --
// indistinguishable from a node that has simply not warmed yet.
func TestAWarmRefusedBecauseOneIsInFlightIsCounted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "envd")
	writeSource(t, src, "binary-v1", time.Unix(1_700_000_000, 0))

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	c := newCacheForTest(t, filepath.Join(dir, "cache"), func(context.Context, string) (string, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release

		return "0.7.0", nil
	})

	fi := mustStat(t, src)
	c.warmAsyncFor(t.Context(), src, fi)
	<-entered

	before := warmsByResult(t)[warmAlreadyWarming]

	// What a node does on every resume while a warm is in flight.
	for range 8 {
		c.warmAsyncFor(t.Context(), src, fi)
	}

	require.Eventually(t, func() bool {
		return warmsByResult(t)[warmAlreadyWarming] >= before+8
	}, 10*time.Second, 10*time.Millisecond,
		"a warm refused because one is already in flight must be counted, not dropped")

	// And the refusal is what it says: one warm of these bytes, not nine.
	c.mu.Lock()
	slots := len(c.warming)
	c.mu.Unlock()
	require.Equal(t, 1, slots, "a refused warm must not claim a slot of its own")

	close(release)
	require.Eventually(t, func() bool {
		_, ok := c.Lookup(src)

		return ok
	}, 10*time.Second, 10*time.Millisecond)
}

// A pin and a suppression are opposites -- one lasts until a promotion, the other
// clears within the minute -- so they cannot share a result. Reported as one
// value, the condition that does not clear on its own reads on a dashboard as
// the one that clears fastest, and the standing advice for diagnosing it points
// at warms{failed}, a series a pinned node never emits again.
func TestAPinnedIdentityIsReportedAsPinnedAndNotAsSuppression(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "envd")
	// A complete image the loader refuses: a verdict about the bytes, which is
	// what pins.
	writeSource(t, src, "binary-v1", time.Unix(1_700_000_000, 0))

	c := newCacheForTest(t, filepath.Join(dir, "cache"), func(_ context.Context, path string) (string, error) {
		return "", errNotExecutable(path)
	})

	// The first encounter is the verdict itself.
	require.ErrorIs(t, c.Warm(t.Context(), src), errBadTarget)

	before := warmsByResult(t)
	c.warmAsyncFor(t.Context(), src, mustStat(t, src))

	require.Eventually(t, func() bool {
		return warmsByResult(t)[warmPinned] >= before[warmPinned]+1
	}, 10*time.Second, 10*time.Millisecond,
		"a refusal to re-copy known-bad bytes must report pinned")

	// And it is not the schedule's suppression: the counter above cannot say that,
	// because the suite is parallel and warms{suppressed} belongs to no one test,
	// so the classification is asserted where it is decided.
	require.Equal(t, warmPinned, warmResult(errBadIdentityKnown))
	require.Equal(t, warmSuppressed, warmResult(errWarmSuppressed),
		"the two conditions are opposites and cannot share a result")
}

// debug/elf is documented as not hardened against malformed input and the
// structural check parses every copy, so a panic there is reachable. Unrecovered,
// it takes the orchestrator down with every sandbox on the node -- and the boot
// warm then parses the same bytes at the same host path, so the restarted process
// dies the same way.
func TestAPanicInsideAWarmIsContainedAndScheduled(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "envd")
	writeSource(t, src, "binary-v1", time.Unix(1_700_000_000, 0))

	c := newCacheForTest(t, filepath.Join(dir, "cache"), func(context.Context, string) (string, error) {
		panic("a parser that does not return")
	})

	err := c.Warm(t.Context(), src)
	require.ErrorIs(t, err, errWarmPanicked, "a panic must come back as an error, not as a dead process")

	// Scheduled, not pinned. A panic says nothing about the artifact: a valid
	// image whose shape the parser mishandles, or a regression in the parser
	// itself, produces one from bytes that would exec perfectly -- and a pin
	// would condemn that artifact on every node that deploys the parser.
	require.Equal(t, warmFailed, warmResult(err), "a panic is a failed copy, not a verdict about the target")
	require.False(t, c.identityKnownBad(src, mustStat(t, src)), "a panic must never pin an identity")
	require.True(t, c.scheduleSuppressed(src), "a panic must arm the retry schedule like any other failure")
}

// Reading the copy before parsing it is what makes the distinction structural
// rather than a taxonomy of error types: the read owns every fault of this node,
// and the parser only ever sees complete bytes. Getting this wrong condemns an
// artifact that is intact in the bucket and warms correctly on every other node,
// permanently, because only a new identity clears a pin.
func TestOnlyTheParseJudgesTheBytes(t *testing.T) {
	t.Parallel()

	full := elfWithPayload("the-whole-binary")

	// Truncation is a verdict: the bytes that arrived are not a complete image,
	// which is the shape a broken promotion commonly takes.
	require.ErrorIs(t, validateImageBytes(t, full[:20]), errBadImage,
		"an image cut inside its header is a verdict about the bytes")

	// A copy this node cannot read is not. The identity says how many bytes the
	// copy carries, so a short one is local damage rather than a truncated
	// source.
	short := filepath.Join(t.TempDir(), "img")
	require.NoError(t, os.WriteFile(short, full, 0o755))
	err := validateImage(short, int64(len(full)+4096))
	require.Error(t, err)
	require.NotErrorIs(t, err, errBadImage,
		"a copy shorter than its identity is this node's fault, not the artifact's")

	// Neither is a copy that cannot be read at all. A directory opens and then
	// fails the read, which is the shape a bad block on the cache device takes --
	// and unlike a mode bit it behaves the same way for root, which is how the
	// orchestrator runs.
	err = validateImage(t.TempDir(), 64)
	require.Error(t, err)
	require.NotErrorIs(t, err, errBadImage,
		"failing to read the copy is about this node and must not pin the source")
}
