package network

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

type StorageLocal struct {
	slotsSize    int
	foreignNs    map[string]struct{}
	acquiredNs   map[string]struct{}
	acquiredNsMu sync.Mutex
	tracer       trace.Tracer
}

const netNamespacesDir = "/var/run/netns"

func NewStorageLocal(slotsSize int, tracer trace.Tracer) (*StorageLocal, error) {
	// get namespaces that we want to always skip
	foreignNs, err := getForeignNamespaces()
	if err != nil {
		return nil, fmt.Errorf("error getting already used namespaces: %v", err)
	}

	foreignNsMap := make(map[string]struct{})
	for _, ns := range foreignNs {
		foreignNsMap[ns] = struct{}{}
		zap.L().Info(fmt.Sprintf("Found foreign namespace: %s", ns))
	}

	return &StorageLocal{
		foreignNs:    foreignNsMap,
		slotsSize:    slotsSize,
		acquiredNs:   make(map[string]struct{}, slotsSize),
		acquiredNsMu: sync.Mutex{},
		tracer:       tracer,
	}, nil
}

func (s *StorageLocal) Acquire() (*Slot, error) {
	acquireTimeoutCtx, acquireCancel := context.WithTimeout(context.Background(), time.Millisecond*500)
	defer acquireCancel()

	_, span := s.tracer.Start(acquireTimeoutCtx, "network-namespace-acquire")
	defer span.End()

	s.acquiredNsMu.Lock()
	defer s.acquiredNsMu.Unlock()

	// we skip the first slot because it's the host slot
	slotIdx := 1

	for {
		select {
		case <-acquireTimeoutCtx.Done():
			return nil, fmt.Errorf("failed to acquire IP slot: timeout")
		default:
			if len(s.acquiredNs) >= s.slotsSize {
				return nil, fmt.Errorf("failed to acquire IP slot: no empty slots found")
			}

			slotIdx++
			slotName := getSlotName(slotIdx)

			// skip the slot if it's already in use by foreign program
			if _, found := s.foreignNs[slotName]; found {
				zap.L().Debug("Skipping slot because already use by foreign program", zap.String("slot", slotName))
				continue
			}

			// skip the slot if it's already acquired
			if _, found := s.acquiredNs[slotName]; found {
				zap.L().Debug("Skipping slot because already acquired", zap.String("slot", slotName))
				continue
			}

			// check if the slot can be acquired
			available, err := isNamespaceAvailable(slotName)
			if err != nil {
				return nil, fmt.Errorf("error checking if namespace is available: %v", err)
			}

			if !available {
				s.foreignNs[slotName] = struct{}{}
				zap.L().Debug("Skipping slot because not available", zap.String("slot", slotName))
				continue
			}

			s.acquiredNs[slotName] = struct{}{}
			slotKey := getMemoryKey(slotIdx)

			return NewSlot(slotKey, slotIdx), nil
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
	nsPath := filepath.Join(netNamespacesDir, name)
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

	files, err := os.ReadDir(netNamespacesDir)
	if err != nil {
		// Folder does not exist, so we can assume no namespaces are in use
		if os.IsNotExist(err) {
			return ns, nil
		}

		return nil, fmt.Errorf("error reading netns directory: %v", err)
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

func getMemoryKey(slotIdx int) string {
	return strconv.Itoa(slotIdx)
}
