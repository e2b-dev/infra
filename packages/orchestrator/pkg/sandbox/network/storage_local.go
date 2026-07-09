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
	config       Config
	slotsSize    int
	foreignNs    map[string]struct{}
	acquiredNs   map[string]struct{}
	acquiredNsMu sync.Mutex
	egressProxy  EgressProxy
}

const NetNamespacesDir = "/var/run/netns"

func NewStorageLocal(ctx context.Context, config Config, egressProxy EgressProxy) (*StorageLocal, error) {
	// get namespaces that we want to always skip
	foreignNs, err := getForeignNamespaces()
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
		foreignNs:    foreignNsMap,
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

	// we skip the first slot because it's the host slot
	slotIdx := 1

	for {
		select {
		case <-acquireTimeoutCtx.Done():
			return nil, errors.New("failed to acquire IP slot: timeout")
		default:
			if len(s.acquiredNs) > s.slotsSize {
				return nil, errors.New("failed to acquire IP slot: no empty slots found")
			}

			slotIdx++
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
			available, err := isNamespaceAvailable(slotName)
			if err != nil {
				return nil, fmt.Errorf("error checking if namespace is available: %w", err)
			}

			if !available {
				s.foreignNs[slotName] = struct{}{}
				logger.L().Debug(ctx, "Skipping slot because not available", zap.String("slot", slotName))

				continue
			}

			s.acquiredNs[slotName] = struct{}{}
			slotKey := getLocalKey(slotIdx)

			return NewSlot(slotKey, slotIdx, s.config, s.egressProxy)
		}
	}
}

func (s *StorageLocal) Release(ips *Slot) error {
	s.acquiredNsMu.Lock()
	defer s.acquiredNsMu.Unlock()

	slotName := getSlotName(ips.Idx)
	delete(s.acquiredNs, slotName)

	return nil
}

func isNamespaceAvailable(name string) (bool, error) {
	nsPath := filepath.Join(NetNamespacesDir, name)
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

func getForeignNamespaces() ([]string, error) {
	var ns []string

	files, err := os.ReadDir(NetNamespacesDir)
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

func NamespaceName(slotIdx int) string {
	return getSlotName(slotIdx)
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
