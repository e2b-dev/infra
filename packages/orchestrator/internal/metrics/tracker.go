package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type Tracker struct {
	selfSandboxResources *smap.Map[sandbox.Config]
	selfWriteInterval    time.Duration
	otherMetrics         map[int]Allocations
	otherLock            sync.RWMutex
}

func (t *Tracker) OnInsert(sandbox *sandbox.Sandbox) {
	t.selfSandboxResources.Insert(sandbox.Metadata.Runtime.SandboxID, sandbox.Config)
}

func (t *Tracker) OnRemove(sandboxID string) {
	t.selfSandboxResources.Remove(sandboxID)
}

func NewTracker(selfWriteInterval time.Duration) (*Tracker, error) {
	return &Tracker{
		otherMetrics: map[int]Allocations{},

		selfWriteInterval:    selfWriteInterval,
		selfSandboxResources: smap.New[sandbox.Config](),
	}, nil
}

func (t *Tracker) TotalRunningCount() int {
	count := t.selfSandboxResources.Count()

	t.otherLock.RLock()
	for _, item := range t.otherMetrics {
		count += int(item.Sandboxes)
	}
	t.otherLock.RUnlock()

	return count
}

func (t *Tracker) getSelfAllocated() Allocations {
	var allocated Allocations
	for _, item := range t.selfSandboxResources.Items() {
		allocated.VCPUs += uint32(item.Vcpu)
		allocated.MemoryBytes += uint64(item.RamMB) * 1024 * 1024
		allocated.DiskBytes += uint64(item.TotalDiskSizeMB) * 1024 * 1024
		allocated.Sandboxes++
	}

	return allocated
}

func (t *Tracker) removeSelfFile(path string) {
	if err := os.Remove(path); err != nil {
		zap.L().Error("Failed to remove self file", zap.Error(err), zap.String("path", path))
	}
}

func (t *Tracker) makeSelfPath(directory string) string {
	filename := fmt.Sprintf("%d.json", os.Getpid())
	selfPath := filepath.Join(directory, filename)

	return selfPath
}

func (t *Tracker) Run(ctx context.Context, directory string) error {
	if err := os.MkdirAll(directory, 0o777); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// set up the self file
	selfPath := t.makeSelfPath(directory)
	defer t.removeSelfFile(selfPath)
	writeTicks := time.Tick(t.selfWriteInterval)

	// set up the file watcher
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	if err = watcher.Add(directory); err != nil {
		if err2 := watcher.Close(); err2 != nil {
			err = errors.Join(err, fmt.Errorf("failed to close watcher: %w", err2))
		}

		return fmt.Errorf("failed to watch %q: %w", directory, err)
	}

	// read existing files
	fullPaths, err := filepath.Glob(filepath.Join(directory, "*.json"))
	if err != nil {
		return fmt.Errorf("failed to read directory: %w", err)
	}
	for _, fullPath := range fullPaths {
		// fullPath := filepath.Join(directory, fullPath)
		if err = t.handleOtherWrite(fullPath); err != nil {
			zap.L().Error("Failed to handle other write",
				zap.Error(err),
				zap.String("path", fullPath))
		}
	}

	// main loop
	// 1. read allocations from other processes
	// 2. write our allocations to a file
	// 3. return when context is canceled
	for {
		select {
		case <-writeTicks:
			if err := t.handleWriteSelf(selfPath); err != nil {
				zap.L().Error("Failed to write allocations",
					zap.Error(err),
					zap.String("path", selfPath))
			}
		case <-ctx.Done():
			err := ctx.Err()
			if err2 := watcher.Close(); err2 != nil {
				err = errors.Join(err, fmt.Errorf("failed to close watcher: %w", err2))
			}

			return err
		case event := <-watcher.Events:
			switch {
			default:
				zap.L().Warn("Unknown event", zap.Any("event", event))
			case event.Name == selfPath:
				// ignore our writes
			case event.Has(fsnotify.Write), event.Has(fsnotify.Create):
				if err := t.handleOtherWrite(event.Name); err != nil {
					zap.L().Error("Failed to handle other write",
						zap.Error(err),
						zap.String("path", event.Name))
				}
			case event.Has(fsnotify.Remove):
				if err := t.handleOtherRemove(event.Name); err != nil {
					zap.L().Error("Failed to handle other remove",
						zap.Error(err),
						zap.String("path", event.Name))
				}
			}
		}
	}
}

func getPIDFromFilename(path string) (int, bool) {
	basePath := filepath.Base(path)
	dotIndex := strings.Index(basePath, ".")
	if dotIndex == -1 {
		zap.L().Warn("Ignoring file without extension", zap.String("file", path))

		return 0, false
	}

	pidStr := basePath[:dotIndex]
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		zap.L().Error("Filename is not a number", zap.String("path", path), zap.Error(err))

		return 0, false
	}

	return pid, true
}

func (t *Tracker) handleOtherRemove(name string) error {
	pid, ok := getPIDFromFilename(name)
	if !ok {
		return errInvalidMetricsFilename
	}

	t.otherLock.Lock()
	defer t.otherLock.Unlock()

	delete(t.otherMetrics, pid)

	return nil
}

var errInvalidMetricsFilename = errors.New("invalid metrics filename")

func (t *Tracker) handleOtherWrite(name string) error {
	pid, ok := getPIDFromFilename(name)
	if !ok {
		return errInvalidMetricsFilename
	}

	data, err := os.ReadFile(name)
	if err != nil {
		return fmt.Errorf("failed to read file: %w", err)
	}

	var allocations Allocations
	if err := json.Unmarshal(data, &allocations); err != nil {
		return fmt.Errorf("failed to unmarshal file: %w", err)
	}

	t.otherLock.Lock()
	defer t.otherLock.Unlock()

	t.otherMetrics[pid] = allocations

	return nil
}

type Allocations struct {
	DiskBytes   uint64 `json:"disk_bytes"`
	MemoryBytes uint64 `json:"memory_bytes"`
	Sandboxes   uint32 `json:"sandboxes"`
	VCPUs       uint32 `json:"vcpus"`
}

func (t *Tracker) TotalAllocated() Allocations {
	allocated := t.getSelfAllocated()

	t.otherLock.RLock()
	for _, item := range t.otherMetrics {
		allocated.VCPUs += item.VCPUs
		allocated.MemoryBytes += item.MemoryBytes
		allocated.DiskBytes += item.DiskBytes
		allocated.Sandboxes += item.Sandboxes
	}
	t.otherLock.RUnlock()

	return allocated
}

func (t *Tracker) handleWriteSelf(selfPath string) error {
	selfAllocated := t.getSelfAllocated()
	data, err := json.Marshal(selfAllocated)
	if err != nil {
		return fmt.Errorf("failed to marshal allocations: %w", err)
	}
	if err := os.WriteFile(selfPath, data, 0o644); err != nil {
		return fmt.Errorf("failed to write allocations: %w", err)
	}

	return nil
}
