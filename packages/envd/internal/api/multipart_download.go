package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/user"
	"strconv"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
	"github.com/e2b-dev/infra/packages/envd/internal/logs"
	"github.com/e2b-dev/infra/packages/envd/internal/permissions"
)

const (
	// maxDownloadSessions limits concurrent download sessions to prevent resource exhaustion
	maxDownloadSessions = 100
	// maxDownloadPartSize limits individual part size to 100MB
	maxDownloadPartSize = 100 * 1024 * 1024
	// defaultDownloadPartSize is the default part size (5MB)
	defaultDownloadPartSize = 5 * 1024 * 1024
	// downloadSessionTTL is the maximum time a download session can remain active
	downloadSessionTTL = 1 * time.Hour
	// downloadSessionCleanupInterval is how often to check for expired sessions
	downloadSessionCleanupInterval = 5 * time.Minute
)

// PostFilesDownloadInit initializes a multipart download session
func (a *API) PostFilesDownloadInit(w http.ResponseWriter, r *http.Request, params PostFilesDownloadInitParams) {
	defer r.Body.Close()

	operationID := logs.AssignOperationID()

	// Parse the request body
	var body PostFilesDownloadInitJSONRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("failed to decode request body")
		jsonError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))

		return
	}

	// Validate signing if needed
	err := a.validateSigning(r, params.Signature, params.SignatureExpiration, params.Username, body.Path, SigningReadOperation)
	if err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error during auth validation")
		jsonError(w, http.StatusUnauthorized, err)

		return
	}

	// Resolve username
	username, err := execcontext.ResolveDefaultUsername(params.Username, a.defaults.User)
	if err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("no user specified")
		jsonError(w, http.StatusBadRequest, err)

		return
	}

	// Lookup user
	u, err := user.Lookup(username)
	if err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Str("username", username).Msg("error looking up user")
		jsonError(w, http.StatusUnauthorized, fmt.Errorf("error looking up user '%s': %w", username, err))

		return
	}

	// Resolve the file path
	filePath, err := permissions.ExpandAndResolve(body.Path, u, a.defaults.Workdir)
	if err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error resolving path")
		jsonError(w, http.StatusBadRequest, fmt.Errorf("error resolving path: %w", err))

		return
	}

	// Check if file exists and get its size
	stat, err := os.Stat(filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Str("path", filePath).Msg("file not found")
			jsonError(w, http.StatusNotFound, fmt.Errorf("file not found: %s", body.Path))

			return
		}
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error checking file")
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error checking file: %w", err))

		return
	}

	// Check if it's a directory
	if stat.IsDir() {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("path", filePath).Msg("path is a directory")
		jsonError(w, http.StatusBadRequest, fmt.Errorf("path is a directory: %s", body.Path))

		return
	}

	totalSize := stat.Size()

	// Determine part size
	partSize := int64(defaultDownloadPartSize)
	if body.PartSize != nil && *body.PartSize > 0 {
		partSize = *body.PartSize
	}
	if partSize > maxDownloadPartSize {
		partSize = maxDownloadPartSize
	}

	// Open the file for reading
	srcFile, err := os.Open(filePath)
	if err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error opening file")
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error opening file: %w", err))

		return
	}

	// Generate download ID
	downloadUUID := uuid.New()
	downloadID := downloadUUID.String()

	// Calculate number of parts
	var numParts int
	if totalSize == 0 {
		numParts = 0 // Empty file, no parts needed
	} else {
		numParts = int((totalSize + partSize - 1) / partSize)
	}

	// Store the session
	session := &MultipartDownloadSession{
		DownloadID: downloadID,
		FilePath:   filePath,
		SrcFile:    srcFile,
		TotalSize:  totalSize,
		PartSize:   partSize,
		NumParts:   numParts,
		CreatedAt:  time.Now(),
	}

	a.downloadsLock.Lock()
	if len(a.downloads) >= maxDownloadSessions {
		a.downloadsLock.Unlock()
		srcFile.Close()
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Int("maxSessions", maxDownloadSessions).Msg("too many concurrent download sessions")
		jsonError(w, http.StatusTooManyRequests, fmt.Errorf("too many concurrent download sessions (max %d)", maxDownloadSessions))

		return
	}
	a.downloads[downloadID] = session
	a.downloadsLock.Unlock()

	a.logger.Debug().
		Str(string(logs.OperationIDKey), operationID).
		Str("downloadId", downloadID).
		Str("filePath", filePath).
		Int64("totalSize", totalSize).
		Int64("partSize", partSize).
		Int("numParts", numParts).
		Msg("multipart download initialized")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(MultipartDownloadInit{
		DownloadId: openapi_types.UUID(downloadUUID),
		TotalSize:  totalSize,
		PartSize:   partSize,
		NumParts:   numParts,
	}); err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("failed to encode response")
	}
}

// GetFilesDownloadDownloadId downloads a specific part of a file
func (a *API) GetFilesDownloadDownloadId(w http.ResponseWriter, r *http.Request, downloadId openapi_types.UUID, params GetFilesDownloadDownloadIdParams) {
	defer r.Body.Close()

	operationID := logs.AssignOperationID()
	downloadIdStr := downloadId.String()

	// Get the session
	a.downloadsLock.RLock()
	session, exists := a.downloads[downloadIdStr]
	a.downloadsLock.RUnlock()

	if !exists {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("downloadId", downloadIdStr).Msg("download session not found")
		jsonError(w, http.StatusNotFound, fmt.Errorf("download session not found: %s", downloadIdStr))

		return
	}

	// Increment active reads counter to prevent file from being closed during read
	session.activeReads.Add(1)
	defer session.activeReads.Add(-1)

	// Check if session is already closed (after incrementing counter to ensure proper ordering)
	if session.closed.Load() {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("downloadId", downloadIdStr).Msg("download session is already closed")
		jsonError(w, http.StatusConflict, fmt.Errorf("download session is already closed"))

		return
	}

	partNumber := params.Part

	// Check for negative part numbers
	if partNumber < 0 {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Int("partNumber", partNumber).Msg("invalid part number")
		jsonError(w, http.StatusBadRequest, fmt.Errorf("part number must be non-negative"))

		return
	}

	// Check part number is within range
	if session.NumParts > 0 && partNumber >= session.NumParts {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Int("partNumber", partNumber).Int("numParts", session.NumParts).Msg("part number out of range")
		jsonError(w, http.StatusBadRequest, fmt.Errorf("part number %d out of range (expected 0-%d)", partNumber, session.NumParts-1))

		return
	}

	// Handle empty file case
	if session.NumParts == 0 {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Msg("cannot download parts from empty file")
		jsonError(w, http.StatusBadRequest, fmt.Errorf("file is empty, no parts to download"))

		return
	}

	// Calculate offset and size for this part
	offset := int64(partNumber) * session.PartSize
	partSize := session.PartSize
	if partNumber == session.NumParts-1 {
		// Last part may be smaller
		partSize = session.TotalSize - offset
	}

	// Get buffer from pool or allocate if needed for larger sizes
	var buffer []byte
	if partSize <= defaultDownloadPartSize {
		poolBuf := a.downloadBuffers.Get().([]byte)
		buffer = poolBuf[:partSize]
		defer a.downloadBuffers.Put(poolBuf[:defaultDownloadPartSize]) // Return full-size buffer to pool
	} else {
		buffer = make([]byte, partSize)
	}

	// Read the part data - ReadAt is thread-safe
	n, err := session.SrcFile.ReadAt(buffer, offset)
	if err != nil && n == 0 {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error reading part data")
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error reading part data: %w", err))

		return
	}

	a.logger.Debug().
		Str(string(logs.OperationIDKey), operationID).
		Str("downloadId", downloadIdStr).
		Int("partNumber", partNumber).
		Int64("offset", offset).
		Int("size", n).
		Msg("part downloaded")

	// Set headers
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(n))
	w.Header().Set("X-Part-Number", strconv.Itoa(partNumber))
	w.Header().Set("X-Part-Size", strconv.Itoa(n))
	w.Header().Set("X-Part-Offset", strconv.FormatInt(offset, 10))
	w.WriteHeader(http.StatusOK)

	// Write the data
	if _, err := w.Write(buffer[:n]); err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error writing response")
	}
}

// DeleteFilesDownloadDownloadId closes a download session and releases resources
func (a *API) DeleteFilesDownloadDownloadId(w http.ResponseWriter, r *http.Request, downloadId openapi_types.UUID) {
	defer r.Body.Close()

	operationID := logs.AssignOperationID()
	downloadIdStr := downloadId.String()

	// Get and remove the session
	a.downloadsLock.Lock()
	session, exists := a.downloads[downloadIdStr]
	if exists {
		// Mark as closed to prevent new reads
		if !session.closed.CompareAndSwap(false, true) {
			// Already being closed by another request
			a.downloadsLock.Unlock()
			a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("downloadId", downloadIdStr).Msg("download session is already closing")
			jsonError(w, http.StatusConflict, fmt.Errorf("download session is already closing"))

			return
		}
		delete(a.downloads, downloadIdStr)
	}
	a.downloadsLock.Unlock()

	if !exists {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("downloadId", downloadIdStr).Msg("download session not found")
		jsonError(w, http.StatusNotFound, fmt.Errorf("download session not found: %s", downloadIdStr))

		return
	}

	// Wait for active reads to complete before closing the file
	for session.activeReads.Load() > 0 {
		time.Sleep(10 * time.Millisecond)
	}

	// Close the file handle
	if err := session.SrcFile.Close(); err != nil {
		a.logger.Warn().Err(err).Str(string(logs.OperationIDKey), operationID).Str("downloadId", downloadIdStr).Msg("error closing file")
	}

	a.logger.Debug().
		Str(string(logs.OperationIDKey), operationID).
		Str("downloadId", downloadIdStr).
		Msg("multipart download closed")

	w.WriteHeader(http.StatusNoContent)
}

// cleanupExpiredDownloads periodically removes expired download sessions
func (a *API) cleanupExpiredDownloads(ctx context.Context) {
	ticker := time.NewTicker(downloadSessionCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Cleanup all remaining sessions on shutdown
			a.downloadsLock.Lock()
			for id, session := range a.downloads {
				if session.closed.CompareAndSwap(false, true) {
					// Wait for active reads
					for session.activeReads.Load() > 0 {
						time.Sleep(10 * time.Millisecond)
					}
					session.SrcFile.Close()
					delete(a.downloads, id)
				}
			}
			a.downloadsLock.Unlock()
			return
		case <-ticker.C:
			a.downloadsLock.Lock()
			now := time.Now()
			for id, session := range a.downloads {
				if now.Sub(session.CreatedAt) > downloadSessionTTL {
					if session.closed.CompareAndSwap(false, true) {
						// Wait for active reads before closing
						for session.activeReads.Load() > 0 {
							time.Sleep(10 * time.Millisecond)
						}
						session.SrcFile.Close()
						delete(a.downloads, id)
						a.logger.Debug().
							Str("downloadId", id).
							Msg("cleaned up expired download session")
					}
				}
			}
			a.downloadsLock.Unlock()
		}
	}
}
