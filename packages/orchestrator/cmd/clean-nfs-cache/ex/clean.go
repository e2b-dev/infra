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
	Experimental           bool
	Path                   string
	AggressiveStat         bool
	BatchN                 int
	DeleteN                int
	TargetBytesToDelete    uint64
	DryRun                 bool
	NumScanners            int
	NumDeleters            int
	MaxErrorRetries        int
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

func (c *Cleaner) Scanner(ctx context.Context, candidateCh chan<- *Candidate, errCh chan<- error, quitCh <-chan struct{}, done *sync.WaitGroup) {
	defer done.Done()
	continuousErrors := 0
	for {
		select {
		case <-quitCh:
			return
		default:
			candidate, err := c.FindCandidate(ctx)
			if err != nil {
				if !errors.Is(err, ErrNoFiles) {
					c.Info(ctx, "error during scanning", zap.Error(err))
				}
				continuousErrors++
				if continuousErrors >= c.MaxErrorRetries {
					errCh <- ErrMaxRetries
					return
				}
				errCh <- err
			} else {
				candidateCh <- candidate
			}
		}
	}
}

func (c *Cleaner) Deleter(ctx context.Context, toDelete <-chan *Candidate, deletedCh chan<- *Candidate, quitCh <-chan struct{}, done *sync.WaitGroup) {
	defer done.Done()
	for {
		select {
		case <-quitCh:
			return
		case d := <-toDelete:
			c.deleteFile(ctx, d, deletedCh)
		}
	}
}

func (c *Cleaner) validateOptions() error {
	if c.Path == "" {
		return ErrUsage
	}
	if c.DeleteN <= 0 {
		return errors.New("deletions-per-loop must be > 0")
	}
	if c.BatchN <= 0 {
		return errors.New("files-per-loop must be > 0")
	}
	if c.BatchN < c.DeleteN {
		return errors.New("files-per-loop must be >= deletions-per-loop")
	}
	if c.TargetBytesToDelete == 0 && c.TargetDiskUsagePercent == 0 {
		return errors.New("either target-bytes-to-delete or disk-usage-target-percent must be set")
	}
	return nil
}

func (c *Cleaner) Clean(ctx context.Context) error {
	if err := c.validateOptions(); err != nil {
		return err
	}

	errCh := make(chan error)
	candidateCh := make(chan *Candidate, 1)
	deleteCh := make(chan *Candidate, c.DeleteN*2)
	deletedNotifyCh := make(chan *Candidate)
	quitCh := make(chan struct{})
	doneCh := make(chan struct{})
	cleanShutdown := &sync.WaitGroup{}

	for i := 0; i < c.NumScanners; i++ {
		cleanShutdown.Add(1)
		go c.Scanner(ctx, candidateCh, errCh, quitCh, cleanShutdown)
	}
	for i := 0; i < c.NumDeleters; i++ {
		cleanShutdown.Add(1)
		go c.Deleter(ctx, deleteCh, deletedNotifyCh, quitCh, cleanShutdown)
	}

	batch := make([]*Candidate, 0, c.DeleteN)
	n := 0

	draining := false
	var result error
	drain := func(err error) {
		if draining {
			return
		}
		close(quitCh)
		draining = true
		result = err

		go func() {
			// wait for scanners and deleters to finish
			cleanShutdown.Wait()
			close(doneCh)
		}()
	}

	for {
		if c.DeletedBytes >= c.TargetBytesToDelete && !draining {
			c.Info(ctx, "target bytes deleted reached, draining remaining candidates")
			drain(nil)
		}

		select {
		case <-doneCh:
			return result

		case <-ctx.Done():
			drain(ctx.Err())

		case candidate := <-candidateCh:
			if draining {
				continue
			}

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
				deleteCh <- toDelete
				total += toDelete.Size
			}
			c.Info(ctx, "deleting files",
				zap.Int("count", c.DeleteN),
				zap.Uint64("bytes", total))
			batch = batch[:0]
			n = 0

		case err := <-errCh:
			if !draining && errors.Is(err, ErrMaxRetries) {
				drain(err)
			}

		case deleted := <-deletedNotifyCh:
			c.DeletedBytes += deleted.Size
			c.DeletedAges = append(c.DeletedAges, time.Duration(deleted.AgeMinutes)*time.Minute)
		}
	}
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

func CreateTestDir(path string, nDirs int, nFiles int, fsize int) {
	os.MkdirAll(path, 0755)

	for i := 0; i < nDirs; i++ {
		dirPath := filepath.Join(path, fmt.Sprintf("dir%d", i))
		err := os.Mkdir(dirPath, 0755)
		if err != nil {
			panic(err)
		}
	}

	for i := 0; i < nFiles; i++ {
		dirPath := filepath.Join(path, fmt.Sprintf("dir%d", i%nDirs))
		filePath := filepath.Join(dirPath, fmt.Sprintf("file%d.txt", i))
		err := os.WriteFile(filePath, []byte(""), 0644)
		if err == nil {
			err = os.Truncate(filePath, int64(fsize))
		}
		if err != nil {
			panic(err)
		}
		tt := time.Now().Add(-1 * time.Duration(i) * time.Minute)
		err = os.Chtimes(filePath, tt, tt)
		if err != nil {
			panic(err)
		}
	}
}
