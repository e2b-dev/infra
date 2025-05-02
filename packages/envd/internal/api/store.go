package api

import (
	"encoding/json"
	"net/http"

	"github.com/e2b-dev/infra/packages/envd/internal/host"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"

	"github.com/rs/zerolog"
)

const (
	memThresholdPct = 80
	cpuThresholdPct = 80
)

type API struct {
	logger      *zerolog.Logger
	accessToken *string
	envVars     *utils.Map[string, string]
}

func New(l *zerolog.Logger, envVars *utils.Map[string, string]) *API {
	return &API{logger: l, envVars: envVars}
}

func (a *API) GetHealth(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	a.logger.Trace().Msg("Health check")

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "")

	w.WriteHeader(http.StatusNoContent)
}

func (a *API) GetMetrics(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	a.logger.Trace().Msg("Get metrics")

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")

	metrics, err := host.GetMetrics()
	if err != nil {
		a.logger.Error().Err(err).Msg("Failed to get metrics")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	memUsedPct := float32(metrics.MemUsedMiB) / float32(metrics.MemTotalMiB) * 100
	if memUsedPct >= memThresholdPct {
		a.logger.Warn().
			Float32("mem_used_percent", memUsedPct).
			Float32("mem_threshold_percent", memThresholdPct).
			Msg("Memory usage threshold exceeded")
	}

	if metrics.CPUUsedPercent >= cpuThresholdPct {
		a.logger.Warn().
			Float32("cpu_used_percent", metrics.CPUUsedPercent).
			Float32("cpu_threshold_percent", cpuThresholdPct).
			Msg("CPU usage threshold exceeded")
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(metrics)
}
