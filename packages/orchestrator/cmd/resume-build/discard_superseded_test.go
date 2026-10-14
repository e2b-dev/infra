package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const testPage = int64(header.PageSize)

// pageHeader builds a header whose mapping is given in page units. Offsets,
// lengths and storage offsets are page counts; the metadata block size is the
// memfile hugepage size, which is also the chunker cache block size.
func pageHeader(t *testing.T, sizePages uint64, entries []header.BuildMap) *header.Header {
	t.Helper()

	scaled := make([]header.BuildMap, len(entries))
	for i, e := range entries {
		scaled[i] = header.BuildMap{
			Offset:             e.Offset * uint64(testPage),
			Length:             e.Length * uint64(testPage),
			BuildId:            e.BuildId,
			BuildStorageOffset: e.BuildStorageOffset * uint64(testPage),
		}
	}
	h, err := header.NewHeader(&header.Metadata{
		Version:   5,
		BlockSize: 2 << 20,
		Size:      sizePages * uint64(testPage),
		BuildId:   uuid.New(),
	}, scaled)
	require.NoError(t, err)

	return h
}

// A dead device page must be discarded at its storage offset, not its device
// offset: the previous layer's chunker cache is addressed in the diff's packed
// storage space. The parent here maps the build to storage offsets that differ
// from its device offsets and jump between mappings; a dead run in the middle
// of a mapping and a dead run spanning two mappings must translate correctly.
func TestSupersededStorageRuns_TranslatesToStorageOffsets(t *testing.T) {
	t.Parallel()

	prev := uuid.New()
	other := uuid.New()

	// prev's own pages, device -> storage, non-contiguous in storage:
	//   dev 0..2 -> sto 50..52   dev 2..4 -> sto 200..202   dev 4..6 -> sto 52..54
	prevHeader := pageHeader(t, 6, []header.BuildMap{
		{Offset: 0, Length: 2, BuildId: prev, BuildStorageOffset: 50},
		{Offset: 2, Length: 2, BuildId: prev, BuildStorageOffset: 200},
		{Offset: 4, Length: 2, BuildId: prev, BuildStorageOffset: 52},
	})

	// The new header keeps dev 0 and dev 5 on prev; dev 1..5 move to other.
	newHeader := pageHeader(t, 6, []header.BuildMap{
		{Offset: 0, Length: 1, BuildId: prev, BuildStorageOffset: 0},
		{Offset: 1, Length: 4, BuildId: other, BuildStorageOffset: 0},
		{Offset: 5, Length: 1, BuildId: prev, BuildStorageOffset: 5},
	})

	// Dead device pages {1,2,3,4} translate to storage {51, 200, 201, 52};
	// live {0,5} -> {50,53}. With one page per block the dead storage pages
	// merge into two runs: [51,53) and [200,202).
	runs := supersededStorageRuns(prevHeader, newHeader, prev, testPage)
	require.Equal(t, []header.BuildMap{
		{Offset: uint64(51 * testPage), Length: uint64(2 * testPage), BuildId: prev},
		{Offset: uint64(200 * testPage), Length: uint64(2 * testPage), BuildId: prev},
	}, runs)
}

// A chunker block is punched only when every page it holds is dead. With
// page-granular mappings a 2 MiB block still mapping one live page must be kept.
func TestSupersededStorageRuns_KeepsBlocksWithAnyLivePage(t *testing.T) {
	t.Parallel()

	const pagesPerBlock = uint64(2<<20) / uint64(header.PageSize) // 512
	prev := uuid.New()
	other := uuid.New()

	// prev owns three contiguous blocks, device == storage.
	prevHeader := pageHeader(t, 3*pagesPerBlock, []header.BuildMap{
		{Offset: 0, Length: 3 * pagesPerBlock, BuildId: prev, BuildStorageOffset: 0},
	})

	// Block 0: live except its last page. Block 1: no live page. Block 2: two
	// scattered live pages.
	lastOfBlock0 := pagesPerBlock - 1
	firstOfBlock2 := 2 * pagesPerBlock
	midOfBlock2 := 2*pagesPerBlock + 100
	newHeader := pageHeader(t, 3*pagesPerBlock, []header.BuildMap{
		{Offset: 0, Length: lastOfBlock0, BuildId: prev},
		{Offset: lastOfBlock0, Length: firstOfBlock2 - lastOfBlock0, BuildId: other},
		{Offset: firstOfBlock2, Length: 1, BuildId: prev},
		{Offset: firstOfBlock2 + 1, Length: midOfBlock2 - firstOfBlock2 - 1, BuildId: other},
		{Offset: midOfBlock2, Length: 1, BuildId: prev},
		{Offset: midOfBlock2 + 1, Length: 3*pagesPerBlock - midOfBlock2 - 1, BuildId: other},
	})

	// Only block 1 is wholly dead.
	runs := supersededStorageRuns(prevHeader, newHeader, prev, 2<<20)
	require.Equal(t, []header.BuildMap{
		{Offset: 2 << 20, Length: 2 << 20, BuildId: prev},
	}, runs)
}

// The discard measurement line carries discard_error only when the discard
// failed, so a refused or failed discard never reads as "nothing superseded".
// A pause benchmark discards once per iteration and the run's exit names
// every failure: a discard that failed in one iteration (the previous diff
// evicted under disk pressure) is not cleared by the next iteration's success,
// and a verify failure is kept the same way.
func TestPauseResultKeepsEveryFailureAcrossIterations(t *testing.T) {
	t.Parallel()

	r := &runner{}
	r.noteDiscard(nil)
	r.noteVerify(nil)
	require.NoError(t, r.pauseResult(), "no failure recorded yet")

	r.noteDiscard(build.ErrDiffNotPresent)
	r.noteDiscard(nil)
	err := r.pauseResult()
	require.ErrorIs(t, err, build.ErrDiffNotPresent, "the first iteration's failure survives the second's success")
	require.ErrorContains(t, err, "discard-superseded failed")

	second := errors.New("second discard failed")
	r.noteDiscard(second)
	err = r.pauseResult()
	require.ErrorIs(t, err, build.ErrDiffNotPresent, "the first failure is still reported")
	require.ErrorIs(t, err, second, "every failure is reported")

	v := &runner{}
	first := errors.New("first verify failed")
	v.noteVerify(first)
	v.noteVerify(nil)
	require.ErrorIs(t, v.pauseResult(), first, "a verify failure survives a later success")
}

// pauseOutcomeFakes are the resolve, discard and capture closures
// recordPauseOutcome takes, replaying one result per discard and capture
// call (what each iteration's discard and each resume of the previous
// generation would return), resolving no runs, and recording the order of
// the calls.
type pauseOutcomeFakes struct {
	discards  []error
	captures  []error
	discarded int
	captured  int
	calls     []string
}

func (f *pauseOutcomeFakes) resolve() (supersededRuns, error) {
	f.calls = append(f.calls, "resolve")

	return supersededRuns{}, nil
}

func (f *pauseOutcomeFakes) discard(supersededRuns) (block.DiscardStats, error) {
	f.calls = append(f.calls, "discard")
	err := f.discards[f.discarded]
	f.discarded++

	return block.DiscardStats{}, err
}

func (f *pauseOutcomeFakes) capture() (string, error) {
	f.calls = append(f.calls, "capture")
	err := f.captures[f.captured]
	f.captured++

	return "", err
}

// recordingRunner is a runner whose machine-readable lines go to the
// returned buffer.
func recordingRunner() (*runner, *bytes.Buffer) {
	var out bytes.Buffer

	return &runner{out: &out}, &out
}

// The baseline is captured before the superseded runs are resolved: the
// resume behind the capture can evict and reload the previous diff, and a
// discard on the handle resolved before it would punch a stale handle while
// the proof reads the one the store serves. The order is capture, resolve,
// discard, and without -verify-previous resolve, discard.
func TestRecordPauseOutcomeCapturesTheBaselineBeforeResolvingTheRuns(t *testing.T) {
	t.Parallel()

	r, _ := recordingRunner()
	opts := pauseOptions{discardSuperseded: true, verifyPrevious: true, verifyCmd: "probe"}
	f := &pauseOutcomeFakes{discards: []error{nil}, captures: []error{nil}}

	r.recordPauseOutcome(t.Context(), opts, f.resolve, f.discard, f.capture)
	require.Equal(t, []string{"capture", "resolve", "discard"}, f.calls, "the baseline capture precedes the resolution, which precedes the discard")

	plain := &pauseOutcomeFakes{discards: []error{nil}}
	r.recordPauseOutcome(t.Context(), pauseOptions{discardSuperseded: true}, plain.resolve, plain.discard, plain.capture)
	require.Equal(t, []string{"resolve", "discard"}, plain.calls, "without -verify-previous there is no capture")
}

// A pause benchmark records each iteration's discard through
// recordPauseOutcome; a discard that failed in the first iteration (the
// previous diff evicted under disk pressure) is still the run's exit error
// after the second iteration's discard succeeded.
func TestRecordPauseOutcomeKeepsTheFirstDiscardFailureAcrossIterations(t *testing.T) {
	t.Parallel()

	r, out := recordingRunner()
	opts := pauseOptions{discardSuperseded: true}
	f := &pauseOutcomeFakes{discards: []error{build.ErrDiffNotPresent, nil}}

	r.recordPauseOutcome(t.Context(), opts, f.resolve, f.discard, f.capture)
	require.ErrorIs(t, r.pauseResult(), build.ErrDiffNotPresent)

	r.recordPauseOutcome(t.Context(), opts, f.resolve, f.discard, f.capture)
	err := r.pauseResult()
	require.ErrorIs(t, err, build.ErrDiffNotPresent, "the first iteration's failure survives the second's success")
	require.ErrorContains(t, err, "discard-superseded failed")
	require.Equal(t, 2, f.discarded, "one discard per iteration")
	require.Equal(t, 0, f.captured, "no resume of the previous generation without -verify-previous")
	require.Equal(t, "discarded_bytes=0 punched_bytes=0 discard_error=diff not present in store\n"+
		"discarded_bytes=0 punched_bytes=0\n", out.String(), "one discard line per iteration, the failure named on its own")
}

// Every verify failure recordPauseOutcome records stays: a failed baseline
// capture (the proof is then skipped and the discard runs plain), a failed
// proof after a captured baseline, and a later failed baseline capture are
// all in the exit error, in that order.
func TestRecordPauseOutcomeKeepsEveryVerifyFailure(t *testing.T) {
	t.Parallel()

	r, out := recordingRunner()
	opts := pauseOptions{discardSuperseded: true, verifyPrevious: true, verifyCmd: "probe"}
	first := errors.New("first baseline capture failed")
	later := errors.New("later baseline capture failed")
	// The second call captures its baseline, so the proof runs; with no
	// superseded run present it fails, before the probe's capture.
	f := &pauseOutcomeFakes{discards: []error{nil, nil, nil}, captures: []error{first, nil, later}}

	for range 3 {
		r.recordPauseOutcome(t.Context(), opts, f.resolve, f.discard, f.capture)
	}
	err := r.pauseResult()
	require.ErrorIs(t, err, first, "the first baseline failure survives the proof and a later baseline failure")
	require.ErrorIs(t, err, later, "the later baseline failure is reported too")
	require.ErrorContains(t, err, "no superseded run was present", "the proof's failure between them is reported")
	require.ErrorContains(t, err, "capture baseline from previous generation")
	require.Equal(t, 3, f.captured, "one baseline capture per call; a failed proof never reaches the probe")
	require.Equal(t, 3, f.discarded, "the discard runs after a failed baseline capture as well as under the proof")
	require.Equal(t, "discarded_bytes=0 punched_bytes=0\n"+
		"discarded_bytes=0 punched_bytes=0\nverify_previous=skipped present_bytes=0\n"+
		"discarded_bytes=0 punched_bytes=0\n", out.String(), "the proof's line only where the proof ran, skipped for want of a present run")
}

// A discard failure recorded under the proof stays through a later call
// whose discard succeeded and whose proof then failed, and a later discard
// failure joins it.
func TestRecordPauseOutcomeKeepsAnEarlierDiscardFailureThroughTheProof(t *testing.T) {
	t.Parallel()

	r, out := recordingRunner()
	opts := pauseOptions{discardSuperseded: true, verifyPrevious: true, verifyCmd: "probe"}
	later := errors.New("later discard failed")
	f := &pauseOutcomeFakes{discards: []error{build.ErrDiffNotPresent, nil, later}, captures: []error{nil, nil, nil}}

	for range 3 {
		r.recordPauseOutcome(t.Context(), opts, f.resolve, f.discard, f.capture)
	}
	err := r.pauseResult()
	require.ErrorIs(t, err, build.ErrDiffNotPresent, "the first discard failure survives a later successful discard")
	require.ErrorIs(t, err, later, "the later discard failure is reported too")
	require.ErrorContains(t, err, "discard-superseded failed")
	require.Equal(t, 3, f.discarded)
	require.Equal(t, 3, f.captured, "a failed discard skips the proof and a failed proof never reaches the probe")
	require.Equal(t, "discarded_bytes=0 punched_bytes=0 discard_error=diff not present in store\nverify_previous=skipped discard_error=diff not present in store\n"+
		"discarded_bytes=0 punched_bytes=0\nverify_previous=skipped present_bytes=0\n"+
		"discarded_bytes=0 punched_bytes=0 discard_error=later discard failed\nverify_previous=skipped discard_error=later discard failed\n", out.String(),
		"a failed discard is named on both lines, the proof skipped for it")
}

// One run can fail both ways: the baseline capture fails, the proof is
// skipped and the plain discard fails too. The exit error names both on one
// line, the discard first, so neither failure hides the other.
func TestRecordPauseOutcomeReportsADiscardAndAVerifyFailureTogether(t *testing.T) {
	t.Parallel()

	r, out := recordingRunner()
	opts := pauseOptions{discardSuperseded: true, verifyPrevious: true, verifyCmd: "probe"}
	baseline := errors.New("baseline capture failed")
	f := &pauseOutcomeFakes{discards: []error{build.ErrDiffNotPresent}, captures: []error{baseline}}

	r.recordPauseOutcome(t.Context(), opts, f.resolve, f.discard, f.capture)
	err := r.pauseResult()
	require.ErrorIs(t, err, build.ErrDiffNotPresent, "the discard failure is in the exit error")
	require.ErrorIs(t, err, baseline, "the verify failure is in the exit error beside it")
	msg := err.Error()
	discardAt := strings.Index(msg, "discard-superseded failed")
	verifyAt := strings.Index(msg, "verify-previous failed")
	require.GreaterOrEqual(t, discardAt, 0)
	require.Greater(t, verifyAt, discardAt, "the discard failure is named first")
	require.NotContains(t, msg, "\n", "both failures on one line")
	require.Equal(t, 1, f.captured, "the proof is skipped after a failed baseline capture")
	require.Equal(t, 1, f.discarded, "the discard still runs")
	require.Equal(t, "discarded_bytes=0 punched_bytes=0 discard_error=diff not present in store\n", out.String(), "the plain discard's line alone, the proof skipped without a line of its own")
}

// A populated proof through the recording method: three superseded runs over
// the fake diff, two present before the discard, the discard on the resolved
// runs, the probe matching its baseline. The two lines are printed in order
// with the exact figures and the run exits clean.
func TestRecordPauseOutcomePrintsAPopulatedProof(t *testing.T) {
	t.Parallel()

	runs := []header.BuildMap{{Offset: 0, Length: 2048}, {Offset: 3072, Length: 1024}, {Offset: 5120, Length: 2048}}
	f := newFakeRefetchDiff(8192, 0, 1, 5, 6)
	r, out := recordingRunner()
	opts := pauseOptions{discardSuperseded: true, verifyPrevious: true, verifyCmd: "probe"}
	resolved := 0
	resolve := func() (supersededRuns, error) {
		resolved++
		s := boundRuns(f, nil, f.blockSize, runs, int64(len(f.data)))
		s.presentWait = 2 * time.Second

		return s, nil
	}
	discard := func(s supersededRuns) (block.DiscardStats, error) {
		return f.discard(s.runs, discardFault{})()
	}
	capture := func() (string, error) { return "a", nil }

	r.recordPauseOutcome(t.Context(), opts, resolve, discard, capture)
	require.NoError(t, r.pauseResult())
	require.Equal(t, 1, resolved)
	require.Equal(t, "discarded_bytes=5120 punched_bytes=5120\n"+
		"verify_previous=ok runs=3 refetched_bytes=4096 present_before=2 punched=5120\n", out.String())
	require.Len(t, f.reads, 10, "five blocks read before the discard and five after")
}

// A nil outcome note leaves a recorded failure untouched: the same error
// value, not a re-wrapped one, so the field holds exactly the failures
// that happened.
func TestNoteNilLeavesARecordedFailureUntouched(t *testing.T) {
	t.Parallel()

	r := &runner{}
	r.noteDiscard(build.ErrDiffNotPresent)
	discardBefore := r.discardErr
	r.noteDiscard(nil)
	require.Same(t, discardBefore, r.discardErr, "the discard failure's value is unchanged")
	require.Equal(t, discardBefore.Error(), r.discardErr.Error())
	require.ErrorIs(t, r.discardErr, build.ErrDiffNotPresent)

	verifyFailed := errors.New("verify failed")
	r.noteVerify(verifyFailed)
	verifyBefore := r.verifyErr
	r.noteVerify(nil)
	require.Same(t, verifyBefore, r.verifyErr, "the verify failure's value is unchanged")
	require.Equal(t, verifyBefore.Error(), r.verifyErr.Error())
	require.ErrorIs(t, r.verifyErr, verifyFailed)
}

func TestDiscardLine(t *testing.T) {
	t.Parallel()

	require.Equal(t, "discarded_bytes=4096 punched_bytes=8192", discardLine(block.DiscardStats{Present: 4096, Punched: 8192}, nil))

	line := discardLine(block.DiscardStats{}, build.ErrDiffStorePinned)
	require.Contains(t, line, "discarded_bytes=0 punched_bytes=0")
	require.Contains(t, line, "discard_error=")
	require.Contains(t, line, "pinned")
}
