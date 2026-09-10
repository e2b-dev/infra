package networkusage

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type faultFile struct {
	bytes.Buffer
	writes, syncs, closes       int
	short                       bool
	writeErr, syncErr, closeErr error
}

func (f *faultFile) Write(p []byte) (int, error) {
	f.writes++
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	if f.short {
		return f.Buffer.Write(p[:len(p)/2])
	}
	return f.Buffer.Write(p)
}
func (f *faultFile) Sync() error  { f.syncs++; return f.syncErr }
func (f *faultFile) Close() error { f.closes++; return f.closeErr }

func memoryJournal() (*Journal, *faultFile) {
	f := &faultFile{}
	now := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	return &Journal{file: f, now: func() time.Time { return now }, last: Record{
		SchemaVersion: 1, SandboxID: "test", Incarnation: "epoch", Valid: true,
	}}, f
}

func records(t *testing.T, data []byte) []Record {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(data))
	var result []Record
	for {
		var r Record
		err := decoder.Decode(&r)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, r)
	}
	return result
}

// NJ1: each flush is a delta; equal successive frames are not duplicates.
func TestConservationAndExactJSON(t *testing.T) {
	j, f := memoryJournal()
	big := uint64(1<<53) + 1
	for _, tx := range []uint64{big, big, 0} {
		if err := j.Observe(tx, 3); err != nil {
			t.Fatal(err)
		}
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	rs := records(t, f.Bytes())
	var tx, rx uint64
	for i, r := range rs {
		tx += r.DeltaTxBytes
		rx += r.DeltaRxBytes
		if r.Sequence != uint64(i+1) || r.TxBytes != tx || r.RxBytes != rx || !r.Valid || r.Complete {
			t.Fatalf("invalid prefix %d: %+v", i, r)
		}
	}
	if tx != 2*big || rx != 9 || f.syncs != 4 {
		t.Fatalf("wrong totals or syncs: %d %d %d", tx, rx, f.syncs)
	}
	if !bytes.Contains(f.Bytes(), []byte(`"deltaTxBytes":"9007199254740993"`)) {
		t.Fatal("lost decimal string precision")
	}
	if rs[3].Reason != "terminal_coverage_unverified" {
		t.Fatal("close claimed terminal evidence")
	}
}

// NJ2/NJ3: failure cannot publish counters or permit a later successful tail.
func TestPersistenceFailuresLatch(t *testing.T) {
	for _, mode := range []string{"short", "write", "sync"} {
		t.Run(mode, func(t *testing.T) {
			j, f := memoryJournal()
			if err := j.Observe(2, 3); err != nil {
				t.Fatal(err)
			}
			before := j.last
			switch mode {
			case "short":
				f.short = true
			case "write":
				f.writeErr = errors.New("disk full")
			case "sync":
				f.syncErr = errors.New("sync failed")
			}
			if err := j.Observe(7, 9); err == nil {
				t.Fatal("acknowledged failed persistence")
			}
			writes := f.writes
			f.short, f.writeErr, f.syncErr = false, nil, nil
			if j.Observe(1, 1) == nil || j.Gap("retry") == nil || j.Close() == nil || j.Close() == nil {
				t.Fatal("failure was not sticky")
			}
			if j.last != before || f.writes != writes || f.closes != 1 {
				t.Fatal("published counters or appended after failure")
			}
		})
	}
}

// NJ4: arithmetic never wraps; valid cannot become true after evidence loss.
func TestOverflowAndGapsAreSticky(t *testing.T) {
	for _, direction := range []string{"tx", "rx"} {
		t.Run(direction, func(t *testing.T) {
			j, f := memoryJournal()
			if direction == "tx" {
				j.last.TxBytes = math.MaxUint64
			} else {
				j.last.RxBytes = math.MaxUint64
			}
			before := j.last
			if j.Observe(1, 1) == nil {
				t.Fatal("overflow accepted")
			}
			if j.last.TxBytes != before.TxBytes || j.last.RxBytes != before.RxBytes || j.last.Valid {
				t.Fatal("overflow altered totals or remained valid")
			}
			if err := j.Observe(0, 0); err != nil {
				t.Fatal(err)
			}
			if err := j.Close(); err != nil {
				t.Fatal(err)
			}
			for _, r := range records(t, f.Bytes()) {
				if r.Valid || r.Complete {
					t.Fatal("invalidity repaired silently")
				}
			}
		})
	}
	j, f := memoryJournal()
	if err := j.Gap("missing_network_counters"); err != nil {
		t.Fatal(err)
	}
	if err := j.Observe(5, 7); err != nil {
		t.Fatal(err)
	}
	if j.last.Valid || j.last.TxBytes != 5 {
		t.Fatal("gap validity or subsequent delta incorrect")
	}
	j.last.Sequence = math.MaxUint64
	writes := f.writes
	if j.Observe(1, 1) == nil || f.writes != writes {
		t.Fatal("sequence wrapped")
	}
}

func TestClockRegressionAndClose(t *testing.T) {
	j, f := memoryJournal()
	if err := j.Observe(2, 3); err != nil {
		t.Fatal(err)
	}
	timestamp := j.lastAt
	j.now = func() time.Time { return timestamp.Add(-time.Second) }
	if err := j.Observe(0, 0); err != nil {
		t.Fatal(err)
	}
	if j.last.Valid || j.last.Reason != "clock_regression" {
		t.Fatal("clock regression hidden")
	}
	j.now = func() time.Time { return timestamp.Add(time.Second) }
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	if j.last.Valid || j.last.Complete || f.closes != 1 || j.Observe(1, 1) == nil {
		t.Fatal("close or sticky validity violated")
	}
	j, f = memoryJournal()
	f.closeErr = errors.New("close failed")
	if j.Close() == nil || j.Close() == nil {
		t.Fatal("close error not retained")
	}
}

// NJ5: identity is per Open, and guest identity cannot select the path.
func TestDurableOpenAndIdentity(t *testing.T) {
	dir := t.TempDir()
	var ids []string
	for range 2 {
		j, err := Open(dir, "../../same-sandbox")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, j.last.Incarnation)
		if err := j.Observe(17, 23); err != nil {
			t.Fatal(err)
		}
		if err := j.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if ids[0] == ids[1] {
		t.Fatal("reused incarnation")
	}
	files, err := filepath.Glob(filepath.Join(dir, "fc-network-*.jsonl"))
	if err != nil || len(files) != 2 {
		t.Fatalf("files: %v %v", files, err)
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		rs := records(t, data)
		if len(rs) != 3 || rs[0].Sequence != 0 || rs[0].Kind != "start" || rs[2].Complete || rs[2].TxBytes != 17 {
			t.Fatalf("bad durable journal: %+v", rs)
		}
		info, err := os.Stat(file)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatal("journal permissions")
		}
	}
	if _, err := Open(filepath.Join(dir, "missing"), "s"); err == nil {
		t.Fatal("created operator directory implicitly")
	}
	if _, err := Open("relative", "s"); err == nil {
		t.Fatal("accepted relative path")
	}
	if _, err := Open(dir, ""); err == nil {
		t.Fatal("accepted empty identity")
	}
	j, err := Open("", "")
	if err != nil || j != nil || j.Observe(1, 2) != nil || j.Gap("disabled") != nil || j.Close() != nil {
		t.Fatal("disabled behavior changed")
	}
}

// NJ6: mutex serialization preserves every accepted sample under concurrency.
func TestConcurrentObservers(t *testing.T) {
	j, f := memoryJournal()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				if err := j.Observe(3, 5); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	rs := records(t, f.Bytes())
	if len(rs) != 801 || j.last.TxBytes != 2400 || j.last.RxBytes != 4000 {
		t.Fatal("lost concurrent samples")
	}
	for i, r := range rs {
		if r.Sequence != uint64(i+1) || r.Complete {
			t.Fatal("invalid sequence or completeness")
		}
	}
}

func TestConcurrentCloseAndGap(t *testing.T) {
	j, f := memoryJournal()
	var accepted atomic.Uint64
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				if j.Observe(3, 5) == nil {
					accepted.Add(1)
				}
			}
		})
	}
	wg.Go(func() { _ = j.Gap("concurrent_gap") })
	for range 3 {
		wg.Go(func() {
			if err := j.Close(); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if j.last.TxBytes != accepted.Load()*3 || j.last.RxBytes != accepted.Load()*5 || f.closes != 1 {
		t.Fatal("lost acknowledgment or repeated close")
	}
	rs := records(t, f.Bytes())
	if rs[len(rs)-1].Kind != "closed" {
		t.Fatal("appended after close")
	}
	for i, r := range rs {
		if r.Sequence != uint64(i+1) || r.Complete {
			t.Fatal("invalid concurrent journal")
		}
	}
}

// This oracle is emitted by verified Dafny Step, not calculated by Go code.
// Run scripts/local-proof/network-journal-model.mjs to reject stale fixtures.
func TestDafnyReplay(t *testing.T) {
	data, err := os.ReadFile("testdata/model.csv")
	if err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil || len(rows) == 0 {
		t.Fatalf("empty/invalid model fixture: %v", err)
	}
	uintAt := func(row []string, i int) uint64 {
		v, err := strconv.ParseUint(row[i], 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	boolAt := func(row []string, i int) bool {
		v, err := strconv.ParseBool(row[i])
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	trace := ""
	var j *Journal
	var f *faultFile
	for i, row := range rows {
		if len(row) != 12 {
			t.Fatalf("bad model row %d", i)
		}
		if row[0] != trace {
			j, f = memoryJournal()
			trace = row[0]
		}
		f.syncErr = nil
		if !boolAt(row, 4) {
			f.syncErr = errors.New("injected durability failure")
		}
		now := time.Date(2026, 9, 10, 0, 0, 0, i+1, time.UTC)
		if !boolAt(row, 5) {
			now = j.lastAt.Add(-time.Second)
		}
		j.now = func() time.Time { return now }
		switch row[1] {
		case "sample":
			_ = j.Observe(uintAt(row, 2), uintAt(row, 3))
		case "gap":
			_ = j.Gap("fixture_gap")
		case "close":
			_ = j.Close()
		default:
			t.Fatalf("unknown action %s", row[1])
		}
		if j.last.Sequence != uintAt(row, 6) || j.last.TxBytes != uintAt(row, 7) || j.last.RxBytes != uintAt(row, 8) ||
			j.last.Valid != boolAt(row, 9) || (j.failed != nil) != boolAt(row, 10) || j.closed != boolAt(row, 11) || j.last.Complete {
			t.Fatalf("Go differs from Dafny at row %d: %v; state=%+v failed=%v closed=%t", i, row, j.last, j.failed, j.closed)
		}
	}
}

func BenchmarkDurableObserve(b *testing.B) {
	j, err := Open(b.TempDir(), "benchmark")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := j.Close(); err != nil {
			b.Error(err)
		}
	})
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := j.Observe(1500, 9000); err != nil {
			b.Fatal(err)
		}
	}
}
