package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog"
	"golang.org/x/sync/semaphore"

	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
	"github.com/e2b-dev/infra/packages/envd/internal/host"
	"github.com/e2b-dev/infra/packages/envd/internal/services/cgroups"
	"github.com/e2b-dev/infra/packages/envd/internal/services/fsfreeze"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"
)

// MMDSClient provides access to MMDS metadata.
type MMDSClient interface {
	GetAccessTokenHash(ctx context.Context) (string, error)
}

// DefaultMMDSClient is the production implementation that calls the real MMDS endpoint.
type DefaultMMDSClient struct{}

func (c *DefaultMMDSClient) GetAccessTokenHash(ctx context.Context) (string, error) {
	return host.GetAccessTokenHashFromMMDS(ctx)
}

type API struct {
	isNotFC     bool
	logger      *zerolog.Logger
	accessToken *SecureToken
	defaults    *execcontext.Defaults

	mmdsChan      chan *host.MMDSOpts
	hyperloopLock sync.Mutex
	mmdsClient    MMDSClient

	lastSetTime *utils.AtomicMax
	initLock    *semaphore.Weighted

	caCertInstaller *host.CACertInstaller
	cgroupManager   cgroups.Manager
	// freezeLock serializes the per-cgroup sweep across /freeze, /unfreeze
	// and the /init deferred unfreeze. PostFreeze acquires with the request
	// ctx; unfreeze paths acquire with Background so they always land
	// regardless of HTTP-client cancellation.
	freezeLock    *semaphore.Weighted
	isMountingNFS atomic.Bool
	mountedPaths  sync.Map // map[path]lifecycleID - tracks which lifecycle each path was mounted for

	// fsFreezer freezes/thaws the guest rootfs for filesystem-only pauses;
	// fsFreezeLock serializes /fsfreeze and /fsthaw.
	fsFreezer    fsfreeze.Freezer
	fsFreezeLock *semaphore.Weighted

	// handover, when non-nil, is the outcome of the live-upgrade handover this
	// envd booted from; PostInit advertises it to the orchestrator via the
	// X-Envd-Handover header. Set once at startup, before serving.
	handover *handoverResult

	// initialized flips true on the first authenticated /init. It gates the
	// live-upgrade /upgrade endpoint and the handover fallback thaw so a
	// re-adopted (possibly hostile) guest process can neither drive an upgrade
	// nor be handed a running workload before /init has re-established auth.
	initialized atomic.Bool
}

// Initialized reports whether the first authenticated /init has completed.
func (a *API) Initialized() bool {
	return a.initialized.Load()
}

// handoverResult is the outcome of a live-upgrade handover, reported to the
// orchestrator on the next /init so the envd-side result (otherwise only logged
// in-guest) is observable fleet-wide.
type handoverResult struct {
	// Every item is total-carried + failed-subset (ok = total - failed).
	Procs          int `json:"procs"`
	ProcsFailed    int `json:"procs_failed"`
	Retained       int `json:"retained"`
	RetainedFailed int `json:"retained_failed"`
	Watchers       int `json:"watchers"`
	WatchersFailed int `json:"watchers_failed"`
}

// SetHandoverResult records the live-upgrade handover outcome so PostInit can
// advertise it. Called once at startup (before serving) when this envd booted
// via --resume-handover.
func (a *API) SetHandoverResult(procs, procsFailed, retained, retainedFailed, watchers, watchersFailed int) {
	a.handover = &handoverResult{
		Procs:          procs,
		ProcsFailed:    procsFailed,
		Retained:       retained,
		RetainedFailed: retainedFailed,
		Watchers:       watchers,
		WatchersFailed: watchersFailed,
	}
}

func New(l *zerolog.Logger, defaults *execcontext.Defaults, mmdsChan chan *host.MMDSOpts, isNotFC bool, cgroupManager cgroups.Manager) *API {
	return &API{
		logger:          l,
		defaults:        defaults,
		mmdsChan:        mmdsChan,
		isNotFC:         isNotFC,
		mmdsClient:      &DefaultMMDSClient{},
		lastSetTime:     utils.NewAtomicMax(),
		accessToken:     &SecureToken{},
		caCertInstaller: host.NewCACertInstaller(l),
		cgroupManager:   cgroupManager,
		initLock:        semaphore.NewWeighted(1),
		freezeLock:      semaphore.NewWeighted(1),
		fsFreezer:       fsfreeze.New(),
		fsFreezeLock:    semaphore.NewWeighted(1),
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
	if err := json.NewEncoder(w).Encode(metrics); err != nil {
		a.logger.Error().Err(err).Msg("Failed to encode metrics")
	}
}
