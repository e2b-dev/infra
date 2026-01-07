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

func TestCompare(t *testing.T) {
	t.Parallel()
	var (
		testFileSize = 7317
		NDirs        = 500
		NFiles       = 10000
		PercentClean = 13
	)

	if testing.Short() {
		NDirs = 5
		NFiles = 100
	}

	ctx := context.Background()
	targetBytesToDelete := uint64(NFiles*testFileSize*PercentClean/100) + 1

	printSummary := func(start time.Time, atimes []time.Duration, deletedBytes uint64) {
		mean, sd := standardDeviation(atimes)
		t.Logf("Cleaned %d (target %d) bytes in %v; file age %v (%v)",
			deletedBytes, targetBytesToDelete, time.Since(start), mean.Round(time.Hour), sd.Round(time.Minute))
	}

	for _, nScan := range []int{1, 4, 16, 32} {
		for _, nDel := range []int{1, 2, 4, 16} {
			for _, nStat := range []int{1, 4, 16, 32} {
				t.Run(fmt.Sprintf("Scan%v-Del%v-Stat%v", nScan, nDel, nStat), func(t *testing.T) {
					t.Parallel()
					path := t.TempDir()
					ex.CreateTestDir(path, NDirs, NFiles, testFileSize)
					t.Cleanup(func() {
						os.RemoveAll(path)
					})
					start := time.Now()
					// log, _ := logger.NewDevelopmentLogger()
					log := logger.NewNopLogger()
					c := ex.NewCleaner(ex.Options{
						Path:                path,
						DeleteN:             NFiles / 100,
						BatchN:              NFiles / 10,
						DryRun:              false,
						MaxConcurrentStat:   nStat,
						MaxConcurrentScan:   nScan,
						MaxConcurrentDelete: nDel,
						TargetBytesToDelete: targetBytesToDelete,
						MaxErrorRetries:     10,
					}, log)
					err := c.Clean(ctx)
					require.NoError(t, err)
					require.GreaterOrEqual(t, c.DeletedBytes.Load(), targetBytesToDelete)
					require.LessOrEqual(t, c.StatxInDirC.Load(), int64(NFiles))
					require.LessOrEqual(t, c.StatxC.Load(), int64(NFiles)+c.DeleteSubmittedC.Load())
					printSummary(start, c.DeletedAge, c.DeletedBytes.Load())
				})
			}
		}
	}

	t.Run("cleanNFSCache", func(t *testing.T) {
		t.Parallel()
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
		}, targetBytesToDelete, 0)
		require.NoError(t, err)
		require.GreaterOrEqual(t, allResults.deletedBytes, targetBytesToDelete)
		printSummary(start, allResults.lastAccessed, uint64(allResults.deletedBytes))
	})
}
