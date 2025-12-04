package main

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/cmd/clean-nfs-cache/ex"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func TestClean(t *testing.T) {
	const (
		testFileSize = 7317
		NDirs        = 500
		NFiles       = 10000
		PercentClean = 13
	)

	ctx := context.Background()

	for _, nScan := range []int{1, 2, 4, 16, 64} {
		for _, nDel := range []int{1, 2, 4, 8} {
			t.Run(fmt.Sprintf("S%v-D%v", nScan, nDel), func(t *testing.T) {
				path := t.TempDir()
				ex.CreateTestDir(path, NDirs, NFiles, testFileSize)
				t.Cleanup(func() {
					os.RemoveAll(path)
				})
				start := time.Now()
				targetBytesToDelete := uint64(NFiles*testFileSize*PercentClean/100) + 1
				c := ex.NewCleaner(ex.Options{
					Path:                path,
					DeleteN:             NFiles / 100,
					BatchN:              NFiles / 10,
					DryRun:              false,
					NumScanners:         nScan,
					NumDeleters:         nDel,
					TargetBytesToDelete: targetBytesToDelete,
					MaxErrorRetries:     10,
				}, logger.NewNopLogger())

				err := c.Clean(ctx)
				require.NoError(t, err)
				require.GreaterOrEqual(t, c.DeletedBytes.Load(), targetBytesToDelete)
				mean, sd := standardDeviation(c.DeletedAges)
				t.Logf("Cleaned %d out of %d bytes in %v with S%d D%d; file age %v (%v)", c.DeletedBytes.Load(), targetBytesToDelete, time.Since(start), nScan, nDel, mean.Round(time.Hour), sd.Round(time.Minute))
			})
		}
	}

	t.Run("cleanNFSCache", func(t *testing.T) {
		path := t.TempDir()
		ex.CreateTestDir(path, NDirs, NFiles, testFileSize)
		t.Cleanup(func() {
			os.RemoveAll(path)
		})

		start := time.Now()
		targetBytesToDelete := int64(NFiles*testFileSize*PercentClean/100) + 1

		allResults, err := cleanNFSCache(ctx, []string{
			"clean-nfs-cache",
			"--dry-run=false",
			fmt.Sprintf("--files-per-loop=%d", NFiles/10),
			fmt.Sprintf("--deletions-per-loop=%d", NFiles/100),
			path,
		}, targetBytesToDelete)
		require.NoError(t, err)
		require.GreaterOrEqual(t, allResults.deletedBytes, targetBytesToDelete)
		mean, sd := standardDeviation(allResults.lastAccessed)
		t.Logf("Cleaned %d out of %d bytes in %v (prior mode); file age %v (%v)", allResults.deletedBytes, targetBytesToDelete, time.Since(start), mean.Round(time.Hour), sd.Round(time.Minute))
	})
}

type DurationHistogram struct {
	bounds []time.Duration
	labels []string
	bytes  []int64
	items  []int64
}

// NewDurationHistogram returns a histogram with buckets starting at 0-10m
// and growing roughly logarithmically up to ">=1y". Use Add() to add one
// duration+size at a time.
func NewDurationHistogram() *DurationHistogram {
	// bucket upper bounds (inclusive): 10m,30m,1h,3h,12h,24h,3d,7d,14d,30d,90d,180d,365d, +Inf
	b := []time.Duration{
		10 * time.Minute,
		30 * time.Minute,
		1 * time.Hour,
		3 * time.Hour,
		12 * time.Hour,
		24 * time.Hour,
		3 * 24 * time.Hour,
		7 * 24 * time.Hour,
		14 * 24 * time.Hour,
		30 * 24 * time.Hour,
		90 * 24 * time.Hour,
		180 * 24 * time.Hour,
		365 * 24 * time.Hour,
	}
	labels := make([]string, len(b)+1)
	var prev time.Duration
	for i, ub := range b {
		labels[i] = formatRange(prev, ub)
		prev = ub
	}
	labels[len(b)] = ">=1y"
	return &DurationHistogram{
		bounds: b,
		labels: labels,
		bytes:  make([]int64, len(labels)),
		items:  make([]int64, len(labels)),
	}
}

// Add records one duration-sized value into the appropriate bucket.
// size is the number of bytes associated with the duration.
func (h *DurationHistogram) Add(d time.Duration, size int64) {
	if size <= 0 {
		return
	}
	for i, ub := range h.bounds {
		if d <= ub {
			h.bytes[i] += size
			h.items[i]++
			return
		}
	}
	// overflow bucket
	last := len(h.bytes) - 1
	h.bytes[last] += size
	h.items[last]++
}

// Labels returns bucket labels in order.
func (h *DurationHistogram) Labels() []string { return h.labels }

// Counts returns a copy of the bytes per bucket (kept the name Counts for compatibility).
func (h *DurationHistogram) Counts() []int64 {
	out := make([]int64, len(h.bytes))
	copy(out, h.bytes)
	return out
}

// String renders the histogram as a table with columns:
// bucket | count | %count | bytes | %bytes
func (h *DurationHistogram) String() string {
	var totalItems int64
	var totalBytes int64
	for i := range h.bytes {
		totalBytes += h.bytes[i]
		totalItems += h.items[i]
	}

	out := ""
	out += fmt.Sprintf("%-12s %12s %9s %14s %9s\n", "bucket", "count", "%count", "bytes", "%bytes")
	out += fmt.Sprintf("%-12s %12s %9s %14s %9s\n", stringsRepeat("-", 12), stringsRepeat("-", 12), stringsRepeat("-", 9), stringsRepeat("-", 14), stringsRepeat("-", 9))
	for i, label := range h.labels {
		cnt := h.items[i]
		b := h.bytes[i]
		var pctCnt float64
		var pctBytes float64
		if totalItems > 0 {
			pctCnt = (float64(cnt) * 100.0) / float64(totalItems)
		}
		if totalBytes > 0 {
			pctBytes = (float64(b) * 100.0) / float64(totalBytes)
		}
		out += fmt.Sprintf("%-12s %12d %8.1f%% %14s %8.1f%%\n", label, cnt, pctCnt, humanBytes(b), pctBytes)
	}
	// Totals line
	out += fmt.Sprintf("%-12s %12d %8s %14s %8s\n", "TOTAL", totalItems, "", humanBytes(totalBytes), "")
	return out
}

// humanBytes renders bytes in a compact form like "1.2MB", "512B", etc.
func humanBytes(b int64) string {
	if b < 0 {
		return "-" + humanBytes(-b)
	}
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	value := float64(b) / float64(div)
	suffix := []string{"KB", "MB", "GB", "TB", "PB", "EB"}[exp]
	return fmt.Sprintf("%.1f%s", value, suffix)
}

// stringsRepeat is a tiny helper to avoid adding an import for strings.
func stringsRepeat(s string, n int) string {
	if n <= 0 {
		return ""
	}
	res := ""
	for i := 0; i < n; i++ {
		res += s
	}
	return res
}

func formatRange(lo, hi time.Duration) string {
	// lo==0 => "0-<hi>"
	if lo == 0 {
		return "<=" + humanDur(hi)
	}
	return humanDur(lo) + "-" + humanDur(hi)
}

func humanDur(d time.Duration) string {
	// produce compact human readable durations like "10m", "3h", "1d"
	if d%(24*time.Hour) == 0 {
		days := int(d / (24 * time.Hour))
		if days == 1 {
			return "1d"
		}
		return fmt.Sprintf("%dd", days)
	}
	if d%(time.Hour) == 0 {
		h := int(d / time.Hour)
		return fmt.Sprintf("%dh", h)
	}
	if d%(time.Minute) == 0 {
		m := int(d / time.Minute)
		return fmt.Sprintf("%dm", m)
	}
	// fallback to seconds
	s := int(d / time.Second)
	return fmt.Sprintf("%ds", s)
}
