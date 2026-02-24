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

// PartStatus represents the state of a multipart upload part.
type PartStatus int

const (
	partPending    PartStatus = iota // zero value: part not yet started
	PartInProgress                   // write currently in flight
	PartComplete                     // write finished successfully
)

// MultipartUploadSession tracks an in-progress multipart upload
type MultipartUploadSession struct {
	UploadID  string
	FilePath  string   // Final destination path
	DestFile  *os.File // Open file handle for direct writes
	TotalSize int64    // Total expected file size (validated >= 0 at input)
	PartSize  int64    // Size of each part (validated > 0 at input)
	NumParts  int      // Total number of expected parts
	UID       int
	GID       int
	Parts     map[int]PartStatus // partNumber -> status
	CreatedAt time.Time
	completed atomic.Bool    // Set to true when complete/abort starts to prevent new parts
	mu        sync.Mutex     // Protects Parts and activeWriters
	wg        sync.WaitGroup // Tracks in-flight part writes; Complete/Delete wait on this before closing DestFile
}

// ignoreNotExist returns nil if err is a "not exist" error, otherwise returns err unchanged.
func ignoreNotExist(err error) error {
	if os.IsNotExist(err) {
		return nil
	}

	return err
}

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
	initLock    sync.Mutex

	// Multipart upload sessions
	uploads     map[string]*MultipartUploadSession
	uploadsLock sync.RWMutex
}

func New(ctx context.Context, l *zerolog.Logger, defaults *execcontext.Defaults, mmdsChan chan *host.MMDSOpts, isNotFC bool) *API {
	api := &API{
		logger:      l,
		defaults:    defaults,
		mmdsChan:    mmdsChan,
		isNotFC:     isNotFC,
		mmdsClient:  &DefaultMMDSClient{},
		lastSetTime: utils.NewAtomicMax(),
		accessToken: &SecureToken{},
		uploads:     make(map[string]*MultipartUploadSession),
	}

	// Start background cleanup for expired upload sessions
	go api.cleanupExpiredUploads(ctx)

	return api
}

// cleanupExpiredUploads periodically removes upload sessions that have exceeded their TTL.
// It stops when ctx is cancelled, preventing goroutine leaks in tests and enabling graceful shutdown.
func (a *API) cleanupExpiredUploads(ctx context.Context) {
	ticker := time.NewTicker(uploadSessionCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.removeExpiredSessions()
		}
	}
}

func (a *API) removeExpiredSessions() {
	a.uploadsLock.Lock()
	defer a.uploadsLock.Unlock()

	now := time.Now()
	for uploadID, session := range a.uploads {
		if now.Sub(session.CreatedAt) > uploadSessionTTL {
			// Mark as completed to prevent races
			if session.completed.CompareAndSwap(false, true) {
				// Unlink the file before removing from the map so a new Init
				// for the same path creates a fresh inode.
				if err := ignoreNotExist(os.Remove(session.FilePath)); err != nil {
					a.logger.Warn().Err(err).Str("filePath", session.FilePath).Msg("failed to cleanup expired upload file")
				}
				delete(a.uploads, uploadID)
				go func(s *MultipartUploadSession) {
					// Wait for any in-flight part writes to finish before closing the descriptor
					s.wg.Wait()
					s.DestFile.Close()
				}(session)
				a.logger.Info().Str("uploadId", uploadID).Msg("cleaned up expired multipart upload session")
			}
		}
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

func (a *API) getLogger(err error) *zerolog.Event {
	if err != nil {
		return a.logger.Error().Err(err) //nolint:zerologlint // this is only prep
	}

	return a.logger.Info() //nolint:zerologlint // this is only prep
}
