package cleaner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
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
	Path    string
	BatchN  int
	DeleteN int

	// TargetFilesToDelete overrides TargetBytesToDelete, and TargetBytesToDelete
	// overrides TargetDiskUsagePercent
	TargetFilesToDelete    uint64
	TargetBytesToDelete    uint64
	TargetDiskUsagePercent float64

	DryRun                bool
	MaxConcurrentStat     int
	MaxConcurrentScan     int
	MaxConcurrentDelete   int
	MaxErrorRetries       int
	OtelCollectorEndpoint string
}

type Counters struct {
	FileC atomic.Int64
	DirC  atomic.Int64

	DeleteSubmittedC   atomic.Int64
	DeleteAttemptC     atomic.Int64
	DeleteErrC         atomic.Int64
	DeleteAlreadyGoneC atomic.Int64
	DeleteSkipC        atomic.Int64
	DeletedBytes       atomic.Uint64
	DeletedAge         []time.Duration

	// Syscalls
	ReadDirC    atomic.Int64
	StatxInDirC atomic.Int64
	StatxC      atomic.Int64
	RemoveC     atomic.Int64
	RemoveDirC  atomic.Int64
}

const (
	dirStateInitial = iota
	dirStateScanning
	dirStatScanned
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
	var errs []error
	if c.DeleteN <= 0 {
		errs = append(errs, errors.New("deletions-per-loop must be > 0"))
	}
	if c.BatchN <= 0 {
		errs = append(errs, errors.New("files-per-loop must be > 0"))
	}
	if c.BatchN < c.DeleteN {
		errs = append(errs, errors.New("files-per-loop must be >= deletions-per-loop"))
	}
	if c.TargetFilesToDelete == 0 && c.TargetBytesToDelete == 0 && c.TargetDiskUsagePercent == 0 {
		errs = append(errs, errors.New("either target-files-to-delete, target-bytes-to-delete or disk-usage-target-percent must be set"))
	}
	if c.MaxConcurrentStat <= 0 {
		errs = append(errs, errors.New("max-concurrent-stat must be > 0"))
	}
	if c.MaxConcurrentScan <= 0 {
		errs = append(errs, errors.New("max-concurrent-scan must be > 0"))
	}
	if c.MaxConcurrentDelete <= 0 {
		errs = append(errs, errors.New("max-concurrent-delete must be > 0"))
	}

	return errors.Join(errs...)
}

func (c *Cleaner) Clean(ctx context.Context) error {
	if err := c.validateOptions(); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(ctx)

	errCh := make(chan error)
	defer close(errCh)
	candidateCh := make(chan *Candidate)
	defer close(candidateCh)
	deleteCh := make(chan *Candidate)
	defer close(deleteCh)
	c.statRequestCh = make(chan *statReq)
	defer close(c.statRequestCh)

	drainedCh := make(chan struct{})
	running := &sync.WaitGroup{}

	batch := make([]*Candidate, 0, c.DeleteN)
	n := 0

	draining := false
	var result error
	drain := func(err error) {
		if draining {
			return
		}

		cancel()
		draining = true
		result = err

		go func() {
			// wait for the running goroutines to finish
			running.Wait()
			close(drainedCh)
		}()
	}

	// Obtain the base level for memory usage
	baseMem := runtime.MemStats{}
	runtime.ReadMemStats(&baseMem)
	batchNumber := 0

	for range c.MaxConcurrentStat {
		running.Add(1)
		go c.Statter(ctx, running)
	}
	for range c.MaxConcurrentScan {
		running.Add(1)
		go c.Scanner(ctx, candidateCh, errCh, running)
	}
	for range c.MaxConcurrentDelete {
		running.Add(1)
		go c.Deleter(ctx, deleteCh, running)
	}

	for {
		if !draining {
			if c.TargetFilesToDelete > 0 {
				if c.RemoveC.Load() >= int64(c.TargetFilesToDelete) {
					c.Info(ctx, "target files deleted reached, draining remaining candidates")
					drain(nil)
				}
			} else {
				if c.DeletedBytes.Load() >= c.TargetBytesToDelete {
					c.Info(ctx, "target bytes deleted reached, draining remaining candidates")
					drain(nil)
				}
			}
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

			// reinsert the "younger" candidates back into the directory tree
			del, reinsertBackToCache := c.splitBatch(batch)

			now := time.Now()
			if len(del) > 0 {
				c.Info(ctx, "delete",
					zap.Int("count", len(del)),
					zap.Duration("oldest", now.Sub(time.Unix(del[0].ATimeUnix, 0))),
					zap.Duration("newest", now.Sub(time.Unix(del[len(del)-1].ATimeUnix, 0))),
					zap.Ints("histogram", c.histogram(del)),
				)
			}
			if len(reinsertBackToCache) > 0 {
				c.Info(ctx, "reinsert",
					zap.Int("count", len(reinsertBackToCache)),
					zap.Duration("oldest", now.Sub(time.Unix(reinsertBackToCache[0].ATimeUnix, 0))),
					zap.Duration("newest", now.Sub(time.Unix(reinsertBackToCache[len(reinsertBackToCache)-1].ATimeUnix, 0))),
					zap.Ints("histogram", c.histogram(reinsertBackToCache)),
				)
			}

			c.reinsertCandidates(reinsertBackToCache)

			total := uint64(0)
			for _, toDelete := range del {
				deleteCh <- toDelete
				c.DeleteSubmittedC.Add(1)
				total += toDelete.Size
			}

			var mem runtime.MemStats
			runtime.ReadMemStats(&mem)
			c.Info(ctx, "memory usage",
				zap.Int("batch", batchNumber),
				zap.Int64("files", c.FileC.Load()),
				zap.Int64("dirs", c.DirC.Load()),
				zap.Uint64("total_alloc", mem.TotalAlloc),
				zap.Uint64("num_gc", uint64(mem.NumGC)),
				zap.Uint64("sys_bytes", mem.Sys-baseMem.Sys),
				zap.Uint64("alloc_bytes", mem.Alloc-baseMem.Alloc),
			)
			c.Info(ctx, "deleting files",
				zap.Int("count", c.DeleteN),
				zap.Uint64("bytes", total))
			batch = batch[:0]
			n = 0
			batchNumber++

		case err := <-errCh:
			if !draining && errors.Is(err, ErrMaxRetries) {
				drain(err)
			}
		}
	}
}

func (c *Cleaner) splitBatch(batch []*Candidate) (toDelete []*Candidate, toReinsert []*Candidate) {
	sort.Slice(batch, func(i, j int) bool {
		return batch[i].ATimeUnix < batch[j].ATimeUnix
	})

	del := min(c.DeleteN, len(batch))
	toDelete = batch[:del]
	toReinsert = batch[del:]

	return toDelete, toReinsert
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
				c.FileC.Add(int64(len(files)))
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
		c.FileC.Add(int64(len(files)))
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

	sort.Slice(d.Files, func(i, j int) bool {
		return d.Files[i].ATimeUnix > d.Files[j].ATimeUnix
	})
}

func (d *Dir) IsScanned() bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.state == dirStatScanned
}

func (d *Dir) isEmpty() bool {
	return d.state == dirStatScanned && len(d.Files) == 0 && len(d.Dirs) == 0
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

func (c *Cleaner) histogram(candidates []*Candidate) []int {
	if len(candidates) == 0 {
		return nil
	}

	buckets := []int64{10, 100, 1000, 10_000, 100_000, 1_000_000, 10_000_000} // seconds
	hist := make([]int, len(buckets)+1)

	now := time.Now().Unix()
	for _, candidate := range candidates {
		age := now - candidate.ATimeUnix
		bucketed := false
		for i, b := range buckets {
			if age <= b {
				hist[i]++
				bucketed = true

				break
			}
		}
		if !bucketed {
			hist[len(buckets)]++
		}
	}

	return hist
}

type DiskInfo struct {
	Total, Used int64
}

func GetDiskInfo(ctx context.Context, path string) (DiskInfo, error) {
	cmd := exec.CommandContext(ctx, "df", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return DiskInfo{}, fmt.Errorf("df command failed: %w: %s", err, strings.TrimSpace(string(out)))
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return DiskInfo{}, fmt.Errorf("unexpected df output: %q", strings.TrimSpace(string(out)))
	}

	// Skip header (line 0) and parse the first data line
	for i := 1; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		totalSize, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return DiskInfo{}, fmt.Errorf("failed to parse total size: %w", err)
		}

		usedSpace, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			return DiskInfo{}, fmt.Errorf("failed to parse available space: %w", err)
		}

		// "df" returns kilobytes, not bytes
		return DiskInfo{Total: totalSize * 1024, Used: usedSpace * 1024}, nil
	}

	return DiskInfo{}, fmt.Errorf("could not parse mount point from df output: %q", strings.TrimSpace(string(out)))
}
