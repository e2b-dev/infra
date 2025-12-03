package ex

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"go.uber.org/zap"
)

type Candidate struct {
	Parent      *Dir
	FullPath    string
	IsDir       bool
	AgeMinutes  uint32
	BAgeMinutes uint32
	Size        uint64
}

type File struct {
	Name       string // short name
	AgeMinutes uint32 // minutes since last access
	Size       uint64
}

type Dir struct {
	Name  string
	Dirs  []Dir
	Files []File
}

type Options struct {
	Experimental           bool
	Path                   string
	AggressiveStat         bool
	BatchN                 int
	DeleteN                int
	TargetBytesToDelete    uint64
	DryRun                 bool
	NumScanners            int
	NumDeleters            int
	TargetDiskUsagePercent float64
	OtelCollectorEndpoint  string
}

type Counters struct {
	SeenDirC  atomic.Uint32
	SeenFileC atomic.Uint64

	DeletedBytes uint64
	DeletedAges  []time.Duration

	// Syscalls
	ReadDirC atomic.Int64
	StatxC   atomic.Int64
	RemoveC  atomic.Int64
}

type Cleaner struct {
	Options
	logger.Logger
	Counters

	mu        sync.RWMutex
	basePath  string
	cacheRoot Dir
}

const maxErrorRetries = 10

var (
	ErrNoFiles    = errors.New("no files found to clean")
	ErrMaxRetries = errors.New("maximum error retries reached")
	ErrUsage      = errors.New("usage: clean-nfs-cache <path> [<options>]")
)

func NewCleaner(opts Options, log logger.Logger) *Cleaner {
	c := &Cleaner{
		Options:  opts,
		Logger:   log,
		mu:       sync.RWMutex{},
		basePath: filepath.Dir(opts.Path),
		cacheRoot: Dir{
			Name: filepath.Base(opts.Path),
		},
	}
	return c
}

func (c *Cleaner) Clean(ctx context.Context) error {
	scanErrCh := make(chan error)
	candidateCh := make(chan *Candidate)
	deletedCh := make(chan *Candidate)
	quitCh := make(chan struct{})

	scanPool := make(chan struct{}, c.NumScanners) // limit concurrency
	for i := 0; i < cap(scanPool); i++ {
		scanPool <- struct{}{}
	}
	deletePool := make(chan struct{}, c.NumDeleters) // limit concurrency
	for i := 0; i < cap(deletePool); i++ {
		deletePool <- struct{}{}
	}

	cleanShutdown := sync.WaitGroup{}

	batch := make([]*Candidate, 0, c.DeleteN)
	continuousErrors := 0
	n := 0

	var result error
	completed := false
	done := false

	drain := func() {
		cleanShutdown.Wait()
		close(quitCh)
	}

LOOP:
	for {
		if !completed {
			switch {
			case c.DeletedBytes >= c.TargetBytesToDelete:
				completed = true // stop making goroutines
				result = nil
				go drain()
			case continuousErrors >= maxErrorRetries:
				completed = true
				result = ErrMaxRetries
				go drain()
			case done:
				completed = true
				// result has been set
				go drain()
			}

		}

		select {
		case <-quitCh:
			break LOOP

		case <-ctx.Done():
			result = ctx.Err()

		case <-scanPool:
			if completed {
				continue // drain remaining goroutines
			}
			cleanShutdown.Add(1)
			go func() {
				defer cleanShutdown.Done()
				candidate, err := c.FindCandidate(ctx)
				if err != nil {
					scanErrCh <- err
				} else {
					candidateCh <- candidate
				}
				scanPool <- struct{}{}
			}()

		case candidate := <-candidateCh:
			if completed {
				continue // drain remaining goroutines
			}

			continuousErrors = 0
			n++
			batch = append(batch, candidate)
			if n < c.BatchN {
				continue
			}

			// Process the batch, start by sorting candidates by age (oldest first)
			sort.Slice(batch, func(i, j int) bool {
				return batch[i].AgeMinutes > batch[j].AgeMinutes
			})

			c.Info(ctx, "selected batch",
				zap.Int("count", len(batch)),
				zap.Uint32("oldest_age_minutes", batch[0].AgeMinutes),
				zap.Uint32("newest_age_minutes", batch[len(batch)-1].AgeMinutes),
			)

			// reinsert the "younger" candidates back into the directory tree
			c.timeit(ctx, "reinserting candidates", func() {
				c.reinsertCandidates(batch[c.DeleteN:])
			})

			total := uint64(0)
			for _, toDelete := range batch[:c.DeleteN] {
				cleanShutdown.Add(1)
				go func() {
					<-deletePool
					defer cleanShutdown.Done()
					c.deleteFile(ctx, toDelete, deletedCh)
					deletePool <- struct{}{}
				}()
				total += toDelete.Size
			}
			c.Info(ctx, "submitted file deletions",
				zap.Int("count", c.DeleteN),
				zap.Uint64("bytes", total))
			batch = batch[:0]
			n = 0

		case err := <-scanErrCh:
			if !errors.Is(err, ErrNoFiles) {
				c.Info(ctx, "error during scanning", zap.Error(err))
			}
			continuousErrors++

		case deleted := <-deletedCh:
			c.DeletedBytes += deleted.Size
			c.DeletedAges = append(c.DeletedAges, time.Duration(deleted.AgeMinutes)*time.Minute)
		}
	}
	return result
}

func (c *Cleaner) reinsertCandidates(candidates []*Candidate) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// sort the candidates by their parent directory so we re-sort each directory only onceœ
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Parent.Name < candidates[j].Parent.Name
	})

	var prevParent *Dir
	for _, candidate := range candidates {
		parent := candidate.Parent

		f := File{
			Name:       filepath.Base(candidate.FullPath),
			AgeMinutes: candidate.AgeMinutes,
			Size:       candidate.Size,
		}
		parent.Files = append(parent.Files, f)

		if prevParent != nil && parent != prevParent {
			prevParent.Sort()
			prevParent = parent
		}
	}
	if prevParent != nil {
		prevParent.Sort()
	}
}

func (c *Cleaner) deleteFile(ctx context.Context, candidate *Candidate, deletedCh chan<- *Candidate) {
	// Best-effort: get current metadata to detect atime changes or if file is gone
	deleted := false
	meta, err := c.stat(candidate.FullPath)
	if err != nil {
		// Already gone?
		deleted = true
		err = nil
	} else if meta.AgeMinutes == candidate.AgeMinutes {
		c.RemoveC.Add(1)
		if !c.DryRun {
			c.timeit(ctx, "deleting file", func() {
				err = os.Remove(candidate.FullPath)
			})
		}
		if err == nil {
			deleted = true
		}
	}

	if deleted {
		deletedCh <- candidate
	}
}

func (c *Cleaner) timeit(ctx context.Context, message string, fn func()) {
	start := time.Now()
	fn()
	done := time.Since(start).Round(time.Millisecond)

	c.Debug(ctx, message, zap.Duration("duration", done))
}
