//go:build linux

package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	consulApi "github.com/hashicorp/consul/api"
)

func TestStorageKVKeyPreservesRollingCompatibility(t *testing.T) {
	storage := &StorageKV{nodeID: "worker-1"}
	if got, want := storage.getKVKey(7), "worker-1/7"; got != want {
		t.Fatalf("getKVKey() = %q, want %q", got, want)
	}
}

type fakeConsulKV struct {
	mu       sync.Mutex
	values   map[string]uint64
	payloads map[string][]byte
	requests []string
	deletes  int
	// replaceOnGet simulates delete-and-recreate between the creating CAS and
	// compensating rollback read.
	replaceOnGet bool
}

func (f *fakeConsulKV) serveHTTP(w http.ResponseWriter, r *http.Request) {
	key, err := url.PathUnescape(strings.TrimPrefix(r.URL.Path, "/v1/kv/"))
	if err != nil {
		http.Error(w, "bad key", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+key)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Consul-Index", "1")

	switch {
	case r.Method == http.MethodPut && r.URL.Query().Get("cas") == "0":
		if _, exists := f.values[key]; exists {
			_, _ = w.Write([]byte("false"))
			return
		}
		value, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			http.Error(w, "bad value", http.StatusBadRequest)
			return
		}
		if f.payloads == nil {
			f.payloads = make(map[string][]byte)
		}
		f.values[key] = 1
		f.payloads[key] = append([]byte(nil), value...)
		_, _ = w.Write([]byte("true"))
	case r.Method == http.MethodGet && r.URL.Query().Has("keys"):
		keys := make([]string, 0, len(f.values))
		for candidate := range f.values {
			if strings.HasPrefix(candidate, key) {
				keys = append(keys, candidate)
			}
		}
		_ = json.NewEncoder(w).Encode(keys)
	case r.Method == http.MethodGet:
		modifyIndex, exists := f.values[key]
		if !exists {
			http.Error(w, "missing", http.StatusNotFound)
			return
		}
		if f.replaceOnGet {
			modifyIndex++
			f.values[key] = modifyIndex
			f.payloads[key] = []byte("replacement-reservation")
			f.replaceOnGet = false
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"Key": key, "ModifyIndex": modifyIndex, "Value": f.payloads[key],
		}})
	case r.Method == http.MethodDelete:
		modifyIndex, exists := f.values[key]
		if !exists || r.URL.Query().Get("cas") != fmt.Sprint(modifyIndex) {
			_, _ = w.Write([]byte("false"))
			return
		}
		delete(f.values, key)
		delete(f.payloads, key)
		f.deletes++
		_, _ = w.Write([]byte("true"))
	default:
		http.Error(w, "unsupported", http.StatusBadRequest)
	}
}

func newFakeStorageKV(t *testing.T, fake *fakeConsulKV, slotsSize int) *StorageKV {
	t.Helper()
	if fake.payloads == nil {
		fake.payloads = make(map[string][]byte)
	}
	server := httptest.NewServer(http.HandlerFunc(fake.serveHTTP))
	t.Cleanup(server.Close)
	config := consulApi.DefaultConfig()
	config.Address = server.URL
	client, err := consulApi.NewClient(config)
	if err != nil {
		t.Fatalf("consulApi.NewClient() error = %v", err)
	}
	return &StorageKV{
		config:       Config{},
		slotsSize:    slotsSize,
		consulClient: client,
		nodeID:       "worker-1",
		egressProxy:  NewNoopEgressProxy(),
		slotFactory: func(key string, slotIdx int) (*Slot, error) {
			return &Slot{Key: key, Idx: slotIdx}, nil
		},
	}
}

func TestStorageKVAcquireNeverReservesSlotZero(t *testing.T) {
	fake := &fakeConsulKV{values: map[string]uint64{}}
	storage := newFakeStorageKV(t, fake, 4)

	slot, err := storage.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if slot.Idx < 1 || slot.Idx >= storage.slotsSize {
		t.Fatalf("Acquire() slot = %d, want [1,%d)", slot.Idx, storage.slotsSize)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	for _, request := range fake.requests {
		if strings.Contains(request, "worker-1/0") {
			t.Fatalf("Acquire() touched invalid slot zero: %s", request)
		}
	}
}

func TestStorageKVAcquireExhaustionNeverTouchesSlotZero(t *testing.T) {
	fake := &fakeConsulKV{values: map[string]uint64{
		"worker-1/1": 1,
		"worker-1/2": 1,
	}}
	storage := newFakeStorageKV(t, fake, 3)

	if _, err := storage.Acquire(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "no empty slots found") {
		t.Fatalf("Acquire() error = %v, want exhaustion", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	for _, request := range fake.requests {
		if strings.Contains(request, "worker-1/0") {
			t.Fatalf("exhausted Acquire() touched invalid slot zero: %s", request)
		}
	}
}

func TestStorageKVRollsBackReservationWhenSlotConstructionFails(t *testing.T) {
	fake := &fakeConsulKV{values: map[string]uint64{}}
	storage := newFakeStorageKV(t, fake, 2)
	storage.slotFactory = func(string, int) (*Slot, error) {
		return nil, errors.New("slot rejected")
	}

	if _, err := storage.Acquire(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "slot rejected") {
		t.Fatalf("Acquire() error = %v, want slot construction failure", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.values) != 0 {
		t.Fatalf("Acquire() leaked reservations: %#v", fake.values)
	}
	if fake.deletes != 1 {
		t.Fatalf("Acquire() compensating deletes = %d, want 1", fake.deletes)
	}
}

func TestStorageKVRollbackCannotDeleteReallocatedReservation(t *testing.T) {
	fake := &fakeConsulKV{
		values:       map[string]uint64{},
		replaceOnGet: true,
	}
	storage := newFakeStorageKV(t, fake, 2)
	storage.slotFactory = func(string, int) (*Slot, error) {
		return nil, errors.New("slot rejected")
	}

	if _, err := storage.Acquire(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "changed before rollback") {
		t.Fatalf("Acquire() error = %v, want replacement-protection failure", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if got := string(fake.payloads["worker-1/1"]); got != "replacement-reservation" {
		t.Fatalf("replacement reservation = %q, want preserved", got)
	}
	if fake.deletes != 0 {
		t.Fatalf("Acquire() deleted a replacement reservation")
	}
}

func TestStorageKVStaleReleaseCannotDeleteReallocatedReservation(t *testing.T) {
	fake := &fakeConsulKV{values: map[string]uint64{}}
	storage := newFakeStorageKV(t, fake, 2)
	slot, err := storage.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}

	fake.mu.Lock()
	fake.values[slot.Key]++
	fake.payloads[slot.Key] = []byte("replacement-reservation")
	fake.mu.Unlock()

	if err := storage.Release(slot); err == nil ||
		!strings.Contains(err.Error(), "already reallocated") {
		t.Fatalf("Release() error = %v, want stale-owner rejection", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if got := string(fake.payloads[slot.Key]); got != "replacement-reservation" {
		t.Fatalf("replacement reservation = %q, want preserved", got)
	}
	if fake.deletes != 0 {
		t.Fatalf("Release() deleted a replacement reservation")
	}
}

func TestStorageKVReleaseDeletesExactReservation(t *testing.T) {
	fake := &fakeConsulKV{values: map[string]uint64{}}
	storage := newFakeStorageKV(t, fake, 2)
	slot, err := storage.Acquire(context.Background())
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	if err := storage.Release(slot); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.values) != 0 || fake.deletes != 1 {
		t.Fatalf("Release() left reservation state: values=%v deletes=%d", fake.values, fake.deletes)
	}
}
