package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/e2b-dev/infra/packages/envd/internal/logs"
	"github.com/e2b-dev/infra/packages/envd/internal/services/memory"
)

// PostCollapse compacts envd's own anonymous heap into 2 MiB transparent
// hugepages just before pause. envd's Go heap arenas are physically scattered
// across many 2 MiB guest-physical frames, each of which is a separate cold
// fault on resume; consolidating them lets envd-init touch far fewer frames.
// Best-effort: a collapse failure is logged but a non-empty result still
// returns success, since the snapshot is taken regardless. The per-call stats
// are returned so the orchestrator can record them as metrics and span
// attributes.
func (a *API) PostCollapse(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	logger := a.logger.With().Str(string(logs.OperationIDKey), logs.AssignOperationID()).Logger()

	start := time.Now()
	stats, err := memory.CollapseSelf(r.Context())
	elapsedMs := time.Since(start).Milliseconds()
	if err != nil {
		logger.Error().Err(err).Int64("elapsed_ms", elapsedMs).Msg("collapse envd heap")
		jsonError(w, http.StatusInternalServerError, err)

		return
	}

	logger.Info().
		Int("regions", stats.Regions).
		Int("chunks", stats.Chunks).
		Int("collapsed", stats.Collapsed).
		Int("already_huge", stats.AlreadyHuge).
		Int("skipped", stats.Skipped).
		Int64("elapsed_ms", elapsedMs).
		Msg("collapsed envd heap")

	result := CollapseResult{
		Regions:     &stats.Regions,
		Chunks:      &stats.Chunks,
		Collapsed:   &stats.Collapsed,
		AlreadyHuge: &stats.AlreadyHuge,
		Skipped:     &stats.Skipped,
		ElapsedMs:   &elapsedMs,
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(result); err != nil {
		logger.Error().Err(err).Msg("encode collapse result")
	}
}
