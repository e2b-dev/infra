package api

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
	"github.com/e2b-dev/infra/packages/envd/internal/host"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"
)

type API struct {
	isNotFC     bool
	logger      *zerolog.Logger
	accessToken *string
	defaults    *execcontext.Defaults

	mmdsChan      chan *host.MMDSOpts
	hyperloopLock sync.Mutex

	lastSetTime *utils.AtomicMax
	initLock    sync.Mutex

	// Tracing
	startupTime     time.Time // When envd process started
	firstInitTime   time.Time // When first /init request was received
	initCompleteTime time.Time // When /init completed
}

func New(l *zerolog.Logger, defaults *execcontext.Defaults, mmdsChan chan *host.MMDSOpts, isNotFC bool, startupTime time.Time) *API {
	return &API{
		logger:      l,
		defaults:    defaults,
		mmdsChan:    mmdsChan,
		isNotFC:     isNotFC,
		lastSetTime: utils.NewAtomicMax(),
		startupTime: startupTime,
	}
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

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(metrics)
}

// TraceInfo contains timing information for debugging resume performance
type TraceInfo struct {
	StartupTimeNs     int64 `json:"startup_time_ns"`      // When envd process started (Unix ns)
	FirstInitTimeNs   int64 `json:"first_init_time_ns"`   // When first /init request received (Unix ns)
	InitCompleteTimeNs int64 `json:"init_complete_time_ns"` // When /init completed (Unix ns)
	CurrentTimeNs     int64 `json:"current_time_ns"`      // Current time (Unix ns)
}

// GetTrace returns timing information for debugging resume performance
func (a *API) GetTrace(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")

	trace := TraceInfo{
		StartupTimeNs:     a.startupTime.UnixNano(),
		FirstInitTimeNs:   a.firstInitTime.UnixNano(),
		InitCompleteTimeNs: a.initCompleteTime.UnixNano(),
		CurrentTimeNs:     time.Now().UnixNano(),
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(trace)
}
