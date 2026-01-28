package api

import (
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

// MultipartUploadSession tracks an in-progress multipart upload
type MultipartUploadSession struct {
	UploadID  string
	FilePath  string // Final destination path
	TempDir   string // Temp directory for parts
	UID       int
	GID       int
	Parts     map[int]string // partNumber -> temp file path
	CreatedAt time.Time
	completed atomic.Bool // Set to true when complete/abort starts to prevent new parts
	mu        sync.Mutex
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

	// Multipart upload sessions
	uploads     map[string]*MultipartUploadSession
	uploadsLock sync.RWMutex
}

func New(l *zerolog.Logger, defaults *execcontext.Defaults, mmdsChan chan *host.MMDSOpts, isNotFC bool) *API {
	// Clean up any stale multipart upload temp directories from previous runs
	if err := os.RemoveAll(multipartTempDir); err != nil {
		l.Warn().Err(err).Str("dir", multipartTempDir).Msg("failed to cleanup stale multipart temp directory")
	}

	api := &API{
		logger:      l,
		defaults:    defaults,
		mmdsChan:    mmdsChan,
		isNotFC:     isNotFC,
		lastSetTime: utils.NewAtomicMax(),
		uploads:     make(map[string]*MultipartUploadSession),
	}

	// Start background cleanup for expired upload sessions
	go api.cleanupExpiredUploads()

	return api
}

// cleanupExpiredUploads periodically removes upload sessions that have exceeded their TTL
func (a *API) cleanupExpiredUploads() {
	ticker := time.NewTicker(uploadSessionCleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		a.uploadsLock.Lock()
		now := time.Now()
		for uploadID, session := range a.uploads {
			if now.Sub(session.CreatedAt) > uploadSessionTTL {
				delete(a.uploads, uploadID)
				// Clean up temp directory in background
				tempDir := session.TempDir
				go func() {
					if err := os.RemoveAll(tempDir); err != nil {
						a.logger.Warn().Err(err).Str("tempDir", tempDir).Msg("failed to cleanup expired upload temp directory")
					}
				}()
				a.logger.Info().Str("uploadId", uploadID).Msg("cleaned up expired multipart upload session")
			}
		}
		a.uploadsLock.Unlock()
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
