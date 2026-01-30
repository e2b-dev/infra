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
	UploadID     string
	FilePath     string   // Final destination path
	DestFile     *os.File // Open file handle for direct writes
	TotalSize    int64    // Total expected file size
	PartSize     int64    // Size of each part (except possibly last)
	NumParts     int      // Total number of expected parts
	UID          int
	GID          int
	PartsWritten map[int]bool // partNumber -> whether it's been written
	CreatedAt    time.Time
	completed    atomic.Bool // Set to true when complete/abort starts to prevent new parts
	mu           sync.Mutex
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
				// Mark as completed to prevent races
				if session.completed.CompareAndSwap(false, true) {
					delete(a.uploads, uploadID)
					// Close file handle and remove file in background
					go func(s *MultipartUploadSession) {
						s.DestFile.Close()
						if err := os.Remove(s.FilePath); err != nil && !os.IsNotExist(err) {
							a.logger.Warn().Err(err).Str("filePath", s.FilePath).Msg("failed to cleanup expired upload file")
						}
					}(session)
					a.logger.Info().Str("uploadId", uploadID).Msg("cleaned up expired multipart upload session")
				}
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
