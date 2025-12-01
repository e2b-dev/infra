package ex

import (
	"context"
	"errors"
	"fmt"
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
	Path           string
	AggressiveStat bool
	BatchN         int
	DeleteN        int
	BytesToDelete  int64
	DryRun         bool
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
	root     Dir
	basePath string
	mu       sync.RWMutex

	Options
	Counters
}

const maxErrorRetries = 10

var (
	ErrNoFiles    = errors.New("no files found to clean")
	ErrMaxRetries = errors.New("maximum error retries reached")
	ErrUsage      = errors.New("usage: clean-nfs-cache <path> [<options>]")
)

func NewCleaner(opts Options) *Cleaner {
	c := &Cleaner{
		Options:  opts,
		mu:       sync.RWMutex{},
		basePath: filepath.Dir(opts.Path),
		root: Dir{
			Name: filepath.Base(opts.Path),
		},
	}
	return c
}

func (c *Cleaner) Clean(ctx context.Context, numScanners int, bytesToDelete uint64) error {
	scanErrCh := make(chan error)
	candidateCh := make(chan *Candidate)
	deletedCh := make(chan *Candidate)
	batchCh := make(chan []*Candidate, 1)
	quitCh := make(chan struct{})

	scanPool := make(chan struct{}, numScanners) // limit concurrency
	for i := 0; i < cap(scanPool); i++ {
		scanPool <- struct{}{}
	}
	clean := sync.WaitGroup{}

	cc := make([]*Candidate, 0, c.DeleteN)
	continuousErrors := 0
	n := 0

	var result error

LOOP:
	for bytesToDelete > c.DeletedBytes {
		start := time.Now()
		select {
		case <-ctx.Done():
			result = ctx.Err()
			break LOOP

		case <-quitCh:
			break LOOP

		case <-scanPool:
			clean.Add(1)
			go func() {
				defer clean.Done()
				candidate, err := c.FindCandidate()
				if err != nil {
					scanErrCh <- err
				} else {
					candidateCh <- candidate
				}
				scanPool <- struct{}{}
			}()

		case candidate := <-candidateCh:
			logger.L().Debug(ctx, "received candidate to delete",
				zap.Duration("waited", time.Since(start)),
				zap.Uint32("age_minutes", candidate.AgeMinutes),
				zap.Uint64("size_bytes", candidate.Size),
				zap.String("name", filepath.Base(candidate.FullPath)))
			continuousErrors = 0
			n++
			cc = append(cc, candidate)
			if n >= c.BatchN {
				batchCh <- cc
				cc = cc[:0]
				n = 0
			}

		case err := <-scanErrCh:
			logger.L().Debug(ctx, "error during scanning",
				zap.Error(err))
			continuousErrors++
			if continuousErrors > 10 {
				result = fmt.Errorf("too many continuous scan errors: %w", ErrMaxRetries)
				close(quitCh)
			}

		case candidates := <-batchCh:
			// sort candidates by age (oldest first)
			sort.Slice(candidates, func(i, j int) bool {
				return candidates[i].AgeMinutes > candidates[j].AgeMinutes
			})

			logger.L().Info(ctx, "selected batch",
				zap.Int("count", len(candidates)),
				zap.Uint32("oldest_age_minutes", candidates[0].AgeMinutes),
				zap.Uint32("newest_age_minutes", candidates[len(candidates)-1].AgeMinutes),
			)

			// reinsert the "younger" candidates back into the directory tree
			timeit(ctx, "reinserting candidates", func() {
				c.reinsertCandidates(candidates[c.DeleteN:])
			})

			for _, candidate := range candidates[:c.DeleteN] {
				clean.Add(1)
				go func(candidate *Candidate) {
					defer clean.Done()
					c.deleteFile(candidate, deletedCh)
				}(candidate)
			}

		case deleted := <-deletedCh:
			logger.L().Debug(ctx, "deleted file",
				zap.String("name", filepath.Base(deleted.FullPath)),
				zap.Uint32("age_minutes", deleted.AgeMinutes),
				zap.Uint64("size_bytes", deleted.Size),
			)
			c.DeletedBytes += deleted.Size
			c.DeletedAges = append(c.DeletedAges, time.Duration(deleted.AgeMinutes)*time.Minute)
		}
	}
	return result
}

func (c *Cleaner) reinsertCandidates(candidates []*Candidate) {
	// sort the candidates by their parent directory so we re-sort each directory only onceœ
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Parent.Name < candidates[j].Parent.Name
	})

	c.mu.Lock()
	defer c.mu.Unlock()

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

func (c *Cleaner) deleteFile(candidate *Candidate, deletedCh chan<- *Candidate) {
	// Best-effort: get current metadata to detect atime changes or if file is gone
	deleted := false
	meta, err := c.fullStat(candidate.FullPath)
	if err != nil {
		// Already gone?
		deleted = true
		err = nil
	} else if meta.AgeMinutes == candidate.AgeMinutes {
		c.RemoveC.Add(1)
		if !c.DryRun {
			err = os.Remove(candidate.FullPath)
		}
		if err == nil {
			deleted = true
		}
	}

	if deleted {
		deletedCh <- candidate
	}
}

func timeit(ctx context.Context, message string, fn func()) {
	start := time.Now()
	fn()
	done := time.Since(start).Round(time.Millisecond)

	logger.L().Debug(ctx, message, zap.Duration("duration", done))
}
