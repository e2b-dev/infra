package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"

	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
	"github.com/e2b-dev/infra/packages/envd/internal/host"
	"github.com/e2b-dev/infra/packages/envd/internal/utils"
)

// MultipartDownloadSession represents an active multipart download session
type MultipartDownloadSession struct {
	DownloadID string
	FilePath   string
	SrcFile    *os.File // Open file handle for ReadAt()
	TotalSize  int64
	PartSize   int64
	NumParts   int
	CreatedAt  time.Time
	closed     atomic.Bool
	activeReads atomic.Int32 // Reference count for active read operations
}

type API struct {
	isNotFC     bool
	logger      *zerolog.Logger
	accessToken *string
	defaults    *execcontext.Defaults

	mmdsChan      chan *host.MMDSOpts
	hyperloopLock sync.Mutex

	lastSetTime *utils.AtomicMax
	initLock    sync.Mutex

	// Multipart download session storage
	downloads       map[string]*MultipartDownloadSession
	downloadsLock   sync.RWMutex
	downloadBuffers sync.Pool // Buffer pool for download operations
	cleanupCancel   context.CancelFunc
}

func New(l *zerolog.Logger, defaults *execcontext.Defaults, mmdsChan chan *host.MMDSOpts, isNotFC bool) *API {
	ctx, cancel := context.WithCancel(context.Background())

	api := &API{
		logger:        l,
		defaults:      defaults,
		mmdsChan:      mmdsChan,
		isNotFC:       isNotFC,
		lastSetTime:   utils.NewAtomicMax(),
		downloads:     make(map[string]*MultipartDownloadSession),
		cleanupCancel: cancel,
		downloadBuffers: sync.Pool{
			New: func() any {
				// Allocate default part size buffer
				return make([]byte, defaultDownloadPartSize)
			},
		},
	}

	// Start cleanup goroutine for expired download sessions
	go api.cleanupExpiredDownloads(ctx)

	return api
}

// Close stops the cleanup goroutine and releases resources
func (a *API) Close() {
	if a.cleanupCancel != nil {
		a.cleanupCancel()
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
