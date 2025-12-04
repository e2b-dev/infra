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

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type Cleaner struct {
	Options
	Counters
	logger.Logger

	mu        sync.RWMutex
	basePath  string
	cacheRoot Dir
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

	DeletedBytes atomic.Uint64
	DeletedAges  []time.Duration

	// Syscalls
	ReadDirC           atomic.Int64
	StatxC             atomic.Int64
	DeleteSubmittedC   atomic.Int64
	DeleteAttemptC     atomic.Int64
	DeleteAlreadyGoneC atomic.Int64
	DeleteChangedMDC   atomic.Int64
	RemoveC            atomic.Int64
	RemoveDirC         atomic.Int64
}

type Dir struct {
	Name  string
	Dirs  []Dir
	Files []File
}

type File struct {
	Name      string // short name
	ATimeUnix int64  // atime in unix seconds
	Size      uint64
}

type Candidate struct {
	Parent    *Dir
	FullPath  string
	IsDir     bool
	ATimeUnix int64
	BTimeUnix int64
	Size      uint64
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

func (c *Cleaner) Clean(ctx context.Context) error {
	if err := c.validateOptions(); err != nil {
		return err
	}

	errCh := make(chan error)
	candidateCh := make(chan *Candidate, 1)
	deleteCh := make(chan *Candidate, c.DeleteN*2)
	quitCh := make(chan struct{})
	doneCh := make(chan struct{})
	cleanShutdown := &sync.WaitGroup{}

	for i := 0; i < c.NumScanners; i++ {
		cleanShutdown.Add(1)
		go c.Scanner(ctx, candidateCh, errCh, quitCh, cleanShutdown)
	}
	for i := 0; i < c.NumDeleters; i++ {
		cleanShutdown.Add(1)
		go c.Deleter(ctx, deleteCh, quitCh, cleanShutdown)
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
		if c.DeletedBytes.Load() >= c.TargetBytesToDelete && !draining {
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
				return batch[i].ATimeUnix > batch[j].ATimeUnix
			})

			c.Info(ctx, "selected batch",
				zap.Int("count", len(batch)),
				zap.Int64("oldest_atime_unix", batch[0].ATimeUnix),
				zap.Int64("newest_atime_unix", batch[len(batch)-1].ATimeUnix),
			)

			// reinsert the "younger" candidates back into the directory tree
			c.timeit(ctx, "reinserting candidates", func() {
				c.reinsertCandidates(batch[c.DeleteN:])
			})

			total := uint64(0)
			for _, toDelete := range batch[:c.DeleteN] {
				deleteCh <- toDelete
				c.DeleteSubmittedC.Add(1)
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
		}
	}
}

func (c *Cleaner) Scanner(ctx context.Context, candidateCh chan<- *Candidate, errCh chan<- error, quitCh <-chan struct{}, done *sync.WaitGroup) {
	defer done.Done()
	continuousErrors := 0
	for {
		select {
		case <-quitCh:
			return
		default:
			var candidate *Candidate
			var err error
			c.timeit(ctx, "finding candidate", func() {
				candidate, err = c.FindCandidate(ctx)
			})
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
				continuousErrors = 0
				candidateCh <- candidate
			}
		}
	}
}

func (c *Cleaner) Deleter(ctx context.Context, toDelete <-chan *Candidate, quitCh <-chan struct{}, done *sync.WaitGroup) {
	defer done.Done()
	for {
		select {
		case <-quitCh:
			return
		case d := <-toDelete:
			c.timeit(ctx, "deleting file", func() {
				c.deleteFile(ctx, d)
			})
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
			Name:      filepath.Base(candidate.FullPath),
			ATimeUnix: candidate.ATimeUnix,
			Size:      candidate.Size,
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

func (c *Cleaner) deleteFile(ctx context.Context, candidate *Candidate) {
	// Best-effort: get current metadata to detect atime changes or if file is gone
	deleted := false
	meta, err := c.stat(candidate.FullPath)
	c.DeleteAttemptC.Add(1)
	if err != nil {
		c.DeleteAlreadyGoneC.Add(1)
		// Already gone?
		deleted = true
		err = nil
	} else if meta.ATimeUnix == candidate.ATimeUnix {
		c.RemoveC.Add(1)
		if !c.DryRun {
			c.timeit(ctx, "deleting file (OS)", func() {
				err = os.Remove(candidate.FullPath)
			})
		}
		if err == nil {
			deleted = true
		}
	} else {
		c.DeleteChangedMDC.Add(1)
	}

	if deleted {
		c.DeletedBytes.Add(candidate.Size)
		c.mu.Lock()
		c.DeletedAges = append(c.DeletedAges, time.Since(time.Unix(candidate.ATimeUnix, 0)))
		c.mu.Unlock()
	}
}

func (c *Cleaner) timeit(ctx context.Context, message string, fn func()) {
	start := time.Now()
	fn()
	done := time.Since(start).Round(time.Millisecond)

	c.Debug(ctx, message, zap.Duration("duration", done))
}

func CreateTestDir(path string, nDirs int, nFiles int, fsize int) {
	os.MkdirAll(path, 0o755)

	for i := 0; i < nDirs; i++ {
		dirPath := filepath.Join(path, fmt.Sprintf("dir%d", i))
		err := os.Mkdir(dirPath, 0o755)
		if err != nil {
			panic(err)
		}
	}

	for i := 0; i < nFiles; i++ {
		dirPath := filepath.Join(path, fmt.Sprintf("dir%d", i%nDirs))
		filePath := filepath.Join(dirPath, fmt.Sprintf("file%d.txt", i))
		err := os.WriteFile(filePath, []byte(""), 0o644)
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
