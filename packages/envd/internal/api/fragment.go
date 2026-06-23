//go:build e2bfragment

// Debug-only (build tag e2bfragment): a /debug/fragment endpoint that inflates
// envd's resident heap so a snapshot captures it, to measure how envd's
// footprint affects resume latency. Never built into production binaries.

package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog"
)

const (
	fragPageSize     = 4096
	fragDefaultMB    = 256
	fragDefaultChunk = 64 // KiB per retained allocation
)

var (
	fragMu       sync.Mutex
	fragRetained [][]byte
)

func RegisterDebugRoutes(m chi.Router, logger *zerolog.Logger) {
	m.Post("/debug/fragment", handleFragment(logger))
}

// handleFragment allocates ?mb MiB in small ?chunk_kb blocks, touches one byte
// per page to make them resident, and retains them for the process lifetime.
func handleFragment(logger *zerolog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mb := queryInt(r, "mb", fragDefaultMB)
		chunkKB := queryInt(r, "chunk_kb", fragDefaultChunk)
		if mb < 0 || chunkKB <= 0 {
			http.Error(w, "mb must be >=0 and chunk_kb >0", http.StatusBadRequest)
			return
		}

		chunkBytes := chunkKB * 1024
		numChunks := (mb * 1024 * 1024) / chunkBytes

		fragMu.Lock()
		for i := range numChunks {
			b := make([]byte, chunkBytes)
			for off := 0; off < len(b); off += fragPageSize {
				b[off] = byte(i + 1)
			}
			fragRetained = append(fragRetained, b)
		}
		totalChunks := len(fragRetained)
		fragMu.Unlock()

		logger.Warn().Int("added_mb", mb).Int("chunk_kb", chunkKB).Int("retained_chunks", totalChunks).Msg("fragmented envd heap")

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]int{
			"added_mb":        mb,
			"chunk_kb":        chunkKB,
			"added_chunks":    numChunks,
			"retained_chunks": totalChunks,
		})
	}
}

func queryInt(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}
