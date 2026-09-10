// Package networkusage preserves host-observed Firecracker byte deltas.
// A journal is local evidence, not a billing receipt or a remote delivery ack.
package networkusage

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"
)

type Record struct {
	SchemaVersion int    `json:"schemaVersion"`
	Kind          string `json:"kind"`
	SandboxID     string `json:"sandboxId"`
	Incarnation   string `json:"incarnation"`
	Sequence      uint64 `json:"sequence,string"`
	ObservedAt    string `json:"observedAt"`
	TxBytes       uint64 `json:"txBytes,string"`
	RxBytes       uint64 `json:"rxBytes,string"`
	DeltaTxBytes  uint64 `json:"deltaTxBytes,string"`
	DeltaRxBytes  uint64 `json:"deltaRxBytes,string"`
	Valid         bool   `json:"valid"`
	Complete      bool   `json:"complete"`
	Reason        string `json:"reason,omitempty"`
}

type durableFile interface {
	io.Writer
	Sync() error
	Close() error
}

// Journal serializes the reader and flusher's evidence into one process epoch.
// A failed write or sync is latched: no later record can hide a partial tail.
type Journal struct {
	mu     sync.Mutex
	file   durableFile
	last   Record
	lastAt time.Time
	now    func() time.Time
	failed error
	closed bool
}

// Open is disabled for an empty directory. A configured directory must already
// exist and be operator-owned; it must not be mounted inside the guest.
func Open(directory, sandboxID string) (*Journal, error) {
	if directory == "" {
		return nil, nil
	}
	if !filepath.IsAbs(directory) || sandboxID == "" || len(sandboxID) > 256 || !utf8.ValidString(sandboxID) {
		return nil, errors.New("network journal requires an absolute directory and bounded sandbox identity")
	}
	dir, err := os.Open(directory)
	if err != nil {
		return nil, fmt.Errorf("open network journal directory: %w", err)
	}
	defer dir.Close()
	id := rand.Text()
	file, err := os.OpenFile(filepath.Join(directory, "fc-network-"+id+".jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create network journal: %w", err)
	}
	j := &Journal{file: file, now: time.Now, last: Record{
		SchemaVersion: 1, SandboxID: sandboxID, Incarnation: id, Valid: true,
	}}
	first := j.last
	first.Kind = "start"
	if err = j.append(first); err == nil {
		err = dir.Sync() // make the newly created directory entry durable too
	}
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("persist network journal start: %w", err)
	}
	return j, nil
}

// append publishes in-memory counters only after the full record is synced.
// Callers hold mu, except Open before the journal is shared.
func (j *Journal) append(record Record) error {
	if j.failed != nil {
		return j.failed
	}
	if j.closed {
		return errors.New("network journal is closed")
	}
	now := j.now().UTC()
	if !j.lastAt.IsZero() && now.Before(j.lastAt) {
		record.Valid = false
		record.Reason = "clock_regression"
	}
	record.ObservedAt = now.Format(time.RFC3339Nano)
	data, err := json.Marshal(record)
	if err == nil {
		data = append(data, '\n')
		var n int
		n, err = j.file.Write(data)
		if err == nil && n != len(data) {
			err = io.ErrShortWrite
		}
	}
	if err == nil {
		err = j.file.Sync()
	}
	if err != nil {
		j.failed = fmt.Errorf("persist network journal: %w", err)
		return j.failed
	}
	j.last = record
	j.lastAt = now
	return nil
}

func (j *Journal) next(kind string) (Record, error) {
	if j.last.Sequence == math.MaxUint64 {
		j.failed = errors.New("network journal sequence overflow")
		return Record{}, j.failed
	}
	r := j.last
	r.Sequence++
	r.Kind, r.Reason = kind, ""
	r.DeltaTxBytes, r.DeltaRxBytes = 0, 0
	return r, nil
}

// Observe adds per-flush deltas, not differences between consecutive frames.
// Nil receivers deliberately preserve the existing disabled configuration.
func (j *Journal) Observe(tx, rx uint64) error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	r, err := j.next("sample")
	if err != nil {
		return err
	}
	if tx > math.MaxUint64-r.TxBytes || rx > math.MaxUint64-r.RxBytes {
		r.Kind, r.Valid, r.Reason = "gap", false, "counter_overflow"
		return errors.Join(errors.New("network byte counter overflow"), j.append(r))
	}
	r.DeltaTxBytes, r.DeltaRxBytes = tx, rx
	r.TxBytes += tx
	r.RxBytes += rx
	return j.append(r)
}

// Gap irreversibly marks this incarnation's observations incomplete.
func (j *Journal) Gap(reason string) error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(reason) == 0 || len(reason) > 80 {
		return errors.New("network journal gap reason must be bounded")
	}
	r, err := j.next("gap")
	if err != nil {
		return err
	}
	r.Valid, r.Reason = false, reason
	return j.append(r)
}

// Close records reader shutdown, never complete terminal network coverage.
// Neither EOF nor fsync proves a final quiescent VM flush or remote durability.
func (j *Journal) Close() error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return j.failed
	}
	r, err := j.next("closed")
	if err == nil {
		r.Reason = "terminal_coverage_unverified"
		err = j.append(r)
	}
	j.closed = true
	j.failed = errors.Join(err, j.file.Close())
	return j.failed
}
