//go:build linux

package network

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type StorageLocal struct {
	config    Config
	slotsSize int
	netnsDir  string
	// foreignNs holds the namespace names found in netnsDir at construction
	// (after startup reclaim); this storage never allocates them. The
	// snapshot is immutable — anything appearing later is handled by the
	// per-scan availability check in Acquire.
	foreignNs map[string]struct{}
	// leakedNs holds indexes Release freed while their namespace still
	// existed — failed teardowns whose namespace RemoveNetwork kept as the
	// reclaim anchor. Acquire falls back to them when no clean index is
	// left, and CreateNetwork finishes the teardown before reuse.
	leakedNs     map[string]struct{}
	acquiredNs   map[string]struct{}
	acquiredNsMu sync.Mutex
	egressProxy  EgressProxy
}

const NetNamespacesDir = "/var/run/netns"

func NewStorageLocal(ctx context.Context, config Config, egressProxy EgressProxy) (*StorageLocal, error) {
	// get namespaces that we want to always skip
	foreignNs, err := getForeignNamespaces(NetNamespacesDir)
	if err != nil {
		return nil, fmt.Errorf("error getting already used namespaces: %w", err)
	}

	foreignNsMap := make(map[string]struct{})
	for _, ns := range foreignNs {
		foreignNsMap[ns] = struct{}{}
		logger.L().Info(ctx, fmt.Sprintf("Found foreign namespace: %s", ns))
	}

	return &StorageLocal{
		config:       config,
		netnsDir:     NetNamespacesDir,
		foreignNs:    foreignNsMap,
		leakedNs:     make(map[string]struct{}),
		slotsSize:    vrtSlotsSize,
		acquiredNs:   make(map[string]struct{}, vrtSlotsSize),
		acquiredNsMu: sync.Mutex{},
		egressProxy:  egressProxy,
	}, nil
}

func (s *StorageLocal) Acquire(ctx context.Context) (*Slot, error) {
	spanCtx, span := tracer.Start(ctx, "network-namespace-acquire")
	defer span.End()

	acquireTimeoutCtx, acquireCancel := context.WithTimeout(spanCtx, time.Millisecond*500)
	defer acquireCancel()

	s.acquiredNsMu.Lock()
	defer s.acquiredNsMu.Unlock()

	// Index 0 is the host slot and below NewSlot's valid range [1, slotsSize].
	for slotIdx := 1; slotIdx <= s.slotsSize; slotIdx++ {
		select {
		case <-acquireTimeoutCtx.Done():
			return nil, fmt.Errorf("failed to acquire IP slot: %w", acquireTimeoutCtx.Err())
		default:
		}

		slotName := getSlotName(slotIdx)

		// skip the slot if it's already in use by foreign program
		if _, found := s.foreignNs[slotName]; found {
			continue
		}

		// skip the slot if it's already acquired
		if _, found := s.acquiredNs[slotName]; found {
			continue
		}

		// check if the slot can be acquired
		available, err := isNamespaceAvailable(s.netnsDir, slotName)
		if err != nil {
			return nil, fmt.Errorf("error checking if namespace is available: %w", err)
		}

		if !available {
			// An existing namespace blocks its index only while it exists:
			// the next scan re-checks, so the index becomes allocatable again
			// once the namespace is gone. Leaks recorded by Release are
			// additionally reclaim candidates below; namespaces this storage
			// never created are not touched.
			logger.L().Debug(ctx, "Skipping network slot: namespace exists", zap.String("slot", slotName))

			continue
		}

		return s.allocate(slotIdx, slotName)
	}

	// Every clean index is taken or blocked. Hand out a leaked index (its
	// namespace survived a failed teardown) so CreateNetwork can finish the
	// teardown and rebuild it — the self-heal the KV storage provided.
	// Preferring clean indexes above keeps a repeatedly failing teardown from
	// starving allocation while capacity remains, and the randomized map
	// order rotates reclaim attempts so one persistently failing leak cannot
	// starve the other leaked indexes.
	for slotName := range s.leakedNs {
		if slotIdx, ok := SlotIndexFromNamespace(slotName); ok {
			return s.allocate(slotIdx, slotName)
		}
	}

	return nil, errors.New("failed to acquire IP slot: no empty slots found")
}

// allocate hands out an index; the caller must hold acquiredNsMu. The slot is
// built first so a NewSlot failure cannot consume the index.
func (s *StorageLocal) allocate(slotIdx int, slotName string) (*Slot, error) {
	slot, err := NewSlot(getLocalKey(slotIdx), slotIdx, s.config, s.egressProxy)
	if err != nil {
		return nil, err
	}

	delete(s.leakedNs, slotName)
	s.acquiredNs[slotName] = struct{}{}

	return slot, nil
}

// Release frees the slot's index unconditionally. If a failed RemoveNetwork
// left the namespace behind as the reclaim anchor, the index is also recorded
// as leaked: it stays blocked while the namespace exists, but once no clean
// index remains Acquire hands it out again and CreateNetwork finishes the
// teardown — transient failures heal in-process instead of draining the pool
// until restart.
func (s *StorageLocal) Release(ips *Slot) error {
	s.acquiredNsMu.Lock()
	defer s.acquiredNsMu.Unlock()

	slotName := getSlotName(ips.Idx)
	delete(s.acquiredNs, slotName)

	available, err := isNamespaceAvailable(s.netnsDir, slotName)
	if err != nil {
		// Indeterminate check: record the leak anyway so the index stays
		// reclaimable instead of stranding behind a namespace nothing
		// retries. A stale entry self-cleans when the index is handed out.
		s.leakedNs[slotName] = struct{}{}

		return fmt.Errorf("error checking namespace of released slot '%s': %w", slotName, err)
	}

	if !available {
		s.leakedNs[slotName] = struct{}{}
	}

	return nil
}

func isNamespaceAvailable(dir, name string) (bool, error) {
	nsPath := filepath.Join(dir, name)
	_, err := os.Stat(nsPath)

	if os.IsNotExist(err) {
		// Namespace does not exist, so it's available
		return true, nil
	} else if err != nil {
		// Some other error
		return false, err
	}

	// File exists so namespace is in use.
	return false, nil
}

func getForeignNamespaces(dir string) ([]string, error) {
	var ns []string

	files, err := os.ReadDir(dir)
	if err != nil {
		// Folder does not exist, so we can assume no namespaces are in use
		if os.IsNotExist(err) {
			return ns, nil
		}

		return nil, fmt.Errorf("error reading netns directory: %w", err)
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		name := file.Name()
		if name == "host" {
			continue
		}

		ns = append(ns, name)
	}

	return ns, nil
}

func getSlotName(slotIdx int) string {
	slotIdxStr := strconv.Itoa(slotIdx)

	return fmt.Sprintf("ns-%s", slotIdxStr)
}

func SlotIndexFromNamespace(name string) (int, bool) {
	idxStr, ok := strings.CutPrefix(name, "ns-")
	if !ok || idxStr == "" {
		return 0, false
	}

	idx, err := strconv.Atoi(idxStr)
	if err != nil || idx < 1 || idx > vrtSlotsSize {
		return 0, false
	}

	return idx, true
}

func ListSlotNamespaces(dir string) ([]int, error) {
	files, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("error reading netns directory: %w", err)
	}

	indices := make([]int, 0, len(files))
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		idx, ok := SlotIndexFromNamespace(file.Name())
		if !ok {
			continue
		}

		indices = append(indices, idx)
	}

	slices.Sort(indices)

	return indices, nil
}

func getLocalKey(slotIdx int) string {
	return strconv.Itoa(slotIdx)
}
