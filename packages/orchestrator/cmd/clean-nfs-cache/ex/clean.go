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

	base          string
	root          *Dir
	statRequestCh chan *statReq
}

type Options struct {
	Experimental           bool
	Path                   string
	BatchN                 int
	DeleteN                int
	TargetBytesToDelete    uint64
	DryRun                 bool
	MaxConcurrentStat      int
	MaxConcurrentScan      int
	MaxConcurrentDelete    int
	MaxErrorRetries        int
	TargetDiskUsagePercent float64
	OtelCollectorEndpoint  string
}

type Counters struct {
	SeenDirC  atomic.Uint32
	SeenFileC atomic.Uint64

	DeleteSubmittedC   atomic.Int64
	DeleteAttemptC     atomic.Int64
	DeleteAlreadyGoneC atomic.Int64
	DeleteSkipC        atomic.Int64
	DeletedBytes       atomic.Uint64
	DeletedAge         []time.Duration

	// Syscalls
	ReadDirC   atomic.Int64
	StatxC     atomic.Int64
	RemoveC    atomic.Int64
	RemoveDirC atomic.Int64
}

const (
	initial = iota
	scanning
	scanned
)

type Dir struct {
	mu    sync.Mutex
	state int

	Name  string
	Dirs  []*Dir
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
	ATimeUnix int64
	BTimeUnix int64
	Size      uint64
}

type statReq struct {
	df       *os.File
	name     string
	response chan *statReq
	f        *File
	err      error
}

var (
	ErrNoFiles    = errors.New("no files found to clean")
	ErrMaxRetries = errors.New("maximum error retries reached")
	ErrBusy       = errors.New("directory is in use by another scanner")
	ErrUsage      = errors.New("usage: clean-nfs-cache <path> [<options>]")
)

func NewCleaner(opts Options, log logger.Logger) *Cleaner {
	c := &Cleaner{
		Options: opts,
		Logger:  log,
		base:    filepath.Dir(opts.Path),
		root:    NewDir(filepath.Base(opts.Path)),
	}

	return c
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
	if c.MaxConcurrentStat <= 0 {
		return errors.New("max-concurrent-stat must be > 0")
	}
	if c.MaxConcurrentScan <= 0 {
		return errors.New("max-concurrent-scan must be > 0")
	}
	if c.MaxConcurrentDelete <= 0 {
		return errors.New("max-concurrent-delete must be > 0")
	}

	return nil
}

func (c *Cleaner) Clean(ctx context.Context) error {
	if err := c.validateOptions(); err != nil {
		return err
	}

	errCh := make(chan error)
	candidateCh := make(chan *Candidate, c.MaxConcurrentScan*2)
	deleteCh := make(chan *Candidate, c.MaxConcurrentDelete*2)
	drainCh := make(chan struct{})
	drainedCh := make(chan struct{})
	cleanShutdown := &sync.WaitGroup{}
	c.statRequestCh = make(chan *statReq)

	batch := make([]*Candidate, 0, c.DeleteN)
	n := 0

	draining := false
	var result error
	drain := func(err error) {
		if draining {
			return
		}
		close(drainCh)
		draining = true
		result = err

		go func() {
			// wait for scanners and deleters to finish
			cleanShutdown.Wait()
			close(drainedCh)
		}()
	}

	for range c.MaxConcurrentStat {
		cleanShutdown.Add(1)
		go c.Statter(ctx, drainCh, cleanShutdown)
	}
	for range c.MaxConcurrentScan {
		cleanShutdown.Add(1)
		go c.Scanner(ctx, candidateCh, errCh, drainCh, cleanShutdown)
	}
	for range c.MaxConcurrentDelete {
		cleanShutdown.Add(1)
		go c.Deleter(ctx, deleteCh, drainCh, cleanShutdown)
	}

	for {
		if c.DeletedBytes.Load() >= c.TargetBytesToDelete && !draining {
			c.Info(ctx, "target bytes deleted reached, draining remaining candidates")
			drain(nil)
		}

		select {
		case <-drainedCh:
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
				return batch[i].ATimeUnix < batch[j].ATimeUnix
			})

			c.Info(ctx, "selected batch",
				zap.Int("count", len(batch)),
				zap.Duration("oldest", time.Since(time.Unix(batch[0].ATimeUnix, 0))),
				zap.Duration("newest", time.Since(time.Unix(batch[len(batch)-1].ATimeUnix, 0))),
			)

			// reinsert the "younger" candidates back into the directory tree
			c.timeit(ctx,
				fmt.Sprintf("reinsert %v candidates", len(batch[c.DeleteN:])), func() {
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

func (c *Cleaner) reinsertCandidates(candidates []*Candidate) {
	// sort the candidates by their parent directory so we lock and re-sort each directory only once.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Parent.Name < candidates[j].Parent.Name
	})
	var prevParent *Dir
	var files []File
	for _, candidate := range candidates {
		parent := candidate.Parent // not nil
		newParent := parent != prevParent
		if newParent {
			if prevParent != nil {
				prevParent.reinsertFiles(files)
			}
			prevParent = parent
			files = files[:0]
		}

		f := File{
			Name:      filepath.Base(candidate.FullPath),
			ATimeUnix: candidate.ATimeUnix,
			Size:      candidate.Size,
		}
		files = append(files, f)
	}
	if prevParent != nil {
		prevParent.reinsertFiles(files)
	}
}

func (c *Cleaner) abs(path []*Dir, name string) string {
	join := c.base
	for _, p := range path {
		join = filepath.Join(join, p.Name)
	}
	if name != "" {
		join = filepath.Join(join, name)
	}

	return join
}

func NewDir(name string) *Dir {
	return &Dir{
		Name: name,
		mu:   sync.Mutex{},
	}
}

func (d *Dir) reinsertFiles(files []File) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.Files = append(d.Files, files...)
	d.sort()
}

func (d *Dir) sort() {
	// sort the dirs by name
	sort.Slice(d.Dirs, func(i, j int) bool {
		return d.Dirs[i].Name < d.Dirs[j].Name
	})

	// sort the files by age, oldest last
	sort.Slice(d.Files, func(i, j int) bool {
		return d.Files[i].ATimeUnix < d.Files[j].ATimeUnix
	})
}

func (d *Dir) IsScanned() bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.state == scanned
}

func (d *Dir) isEmpty() bool {
	return d.state == scanned && len(d.Files) == 0 && len(d.Dirs) == 0
}

func (d *Dir) IsEmpty() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.isEmpty()
}

func (c *Cleaner) timeit(ctx context.Context, message string, fn func()) {
	start := time.Now()
	fn()
	done := time.Since(start).Round(time.Millisecond)

	c.Debug(ctx, message, zap.Duration("duration", done))
}

func CreateTestDir(path string, nDirs int, nFiles int, fsize int) {
	os.MkdirAll(path, 0o755)

	for i := range nDirs {
		dirPath := filepath.Join(path, fmt.Sprintf("dir%d", i))
		err := os.Mkdir(dirPath, 0o755)
		if err != nil {
			panic(err)
		}
	}

	for i := range nFiles {
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
