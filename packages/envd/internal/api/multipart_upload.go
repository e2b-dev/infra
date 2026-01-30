package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
	"github.com/e2b-dev/infra/packages/envd/internal/logs"
	"github.com/e2b-dev/infra/packages/envd/internal/permissions"
)

const (
	// maxUploadSessions limits concurrent upload sessions to prevent resource exhaustion
	maxUploadSessions = 100
	// maxPartSize limits individual part size to 100MB to prevent DoS
	maxPartSize = 100 * 1024 * 1024
	// uploadSessionTTL is the maximum time an upload session can remain active
	uploadSessionTTL = 1 * time.Hour
	// uploadSessionCleanupInterval is how often to check for expired sessions
	uploadSessionCleanupInterval = 5 * time.Minute
)

// PostFilesUploadInit initializes a multipart upload session
func (a *API) PostFilesUploadInit(w http.ResponseWriter, r *http.Request, params PostFilesUploadInitParams) {
	defer r.Body.Close()

	operationID := logs.AssignOperationID()

	// Parse the request body
	var body PostFilesUploadInitJSONRequestBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("failed to decode request body")
		jsonError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))

		return
	}

	// Validate totalSize and partSize
	if body.TotalSize < 0 {
		jsonError(w, http.StatusBadRequest, fmt.Errorf("totalSize must be non-negative"))

		return
	}
	if body.PartSize <= 0 {
		jsonError(w, http.StatusBadRequest, fmt.Errorf("partSize must be positive"))

		return
	}
	if body.PartSize > maxPartSize {
		jsonError(w, http.StatusBadRequest, fmt.Errorf("partSize exceeds maximum allowed size of %d bytes", maxPartSize))

		return
	}

	// Check session limit early before doing any file operations
	a.uploadsLock.RLock()
	sessionCount := len(a.uploads)
	a.uploadsLock.RUnlock()
	if sessionCount >= maxUploadSessions {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Int("maxSessions", maxUploadSessions).Msg("too many concurrent upload sessions")
		jsonError(w, http.StatusTooManyRequests, fmt.Errorf("too many concurrent upload sessions (max %d)", maxUploadSessions))

		return
	}

	// Validate signing if needed
	err := a.validateSigning(r, params.Signature, params.SignatureExpiration, params.Username, body.Path, SigningWriteOperation)
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

	uid, gid, err := permissions.GetUserIdInts(u)
	if err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error getting user ids")
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error getting user ids: %w", err))

		return
	}

	// Resolve the file path
	filePath, err := permissions.ExpandAndResolve(body.Path, u, a.defaults.Workdir)
	if err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error resolving path")
		jsonError(w, http.StatusBadRequest, fmt.Errorf("error resolving path: %w", err))

		return
	}

	// Ensure parent directories exist
	if err := permissions.EnsureDirs(filepath.Dir(filePath), uid, gid); err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error ensuring directories")
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error ensuring directories: %w", err))

		return
	}

	// Create and preallocate the destination file
	destFile, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		if errors.Is(err, syscall.ENOSPC) {
			a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("not enough disk space")
			jsonError(w, http.StatusInsufficientStorage, fmt.Errorf("not enough disk space"))

			return
		}
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error creating destination file")
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error creating destination file: %w", err))

		return
	}

	// Preallocate the file to the total size (creates sparse file)
	if body.TotalSize > 0 {
		if err := destFile.Truncate(body.TotalSize); err != nil {
			destFile.Close()
			os.Remove(filePath)
			if errors.Is(err, syscall.ENOSPC) {
				a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("not enough disk space")
				jsonError(w, http.StatusInsufficientStorage, fmt.Errorf("not enough disk space"))

				return
			}
			a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error preallocating file")
			jsonError(w, http.StatusInternalServerError, fmt.Errorf("error preallocating file: %w", err))

			return
		}
	}

	// Set ownership
	if err := os.Chown(filePath, uid, gid); err != nil {
		destFile.Close()
		os.Remove(filePath)
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error changing file ownership")
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error changing file ownership: %w", err))

		return
	}

	// Create upload ID
	uploadID := uuid.New().String()

	// Calculate number of parts
	numParts := int((body.TotalSize + body.PartSize - 1) / body.PartSize)
	if numParts == 0 && body.TotalSize == 0 {
		numParts = 0 // Empty file, no parts needed
	}

	// Store the session with the open file handle
	session := &MultipartUploadSession{
		UploadID:     uploadID,
		FilePath:     filePath,
		DestFile:     destFile,
		TotalSize:    body.TotalSize,
		PartSize:     body.PartSize,
		NumParts:     numParts,
		UID:          uid,
		GID:          gid,
		PartsWritten: make(map[int]bool),
		CreatedAt:    time.Now(),
	}

	a.uploadsLock.Lock()
	if len(a.uploads) >= maxUploadSessions {
		a.uploadsLock.Unlock()
		destFile.Close()
		os.Remove(filePath)
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Int("maxSessions", maxUploadSessions).Msg("too many concurrent upload sessions")
		jsonError(w, http.StatusTooManyRequests, fmt.Errorf("too many concurrent upload sessions (max %d)", maxUploadSessions))

		return
	}
	a.uploads[uploadID] = session
	a.uploadsLock.Unlock()

	a.logger.Debug().
		Str(string(logs.OperationIDKey), operationID).
		Str("uploadId", uploadID).
		Str("filePath", filePath).
		Int64("totalSize", body.TotalSize).
		Int64("partSize", body.PartSize).
		Int("numParts", numParts).
		Msg("multipart upload initialized")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(MultipartUploadInit{
		UploadId: uploadID,
	}); err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("failed to encode response")
	}
}

// PutFilesUploadUploadId uploads a part of a multipart upload directly to the destination file
func (a *API) PutFilesUploadUploadId(w http.ResponseWriter, r *http.Request, uploadId string, params PutFilesUploadUploadIdParams) {
	defer r.Body.Close()

	operationID := logs.AssignOperationID()

	// Validate uploadId is a valid UUID to prevent path traversal
	if _, err := uuid.Parse(uploadId); err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Msg("invalid upload ID format")
		jsonError(w, http.StatusBadRequest, fmt.Errorf("invalid upload ID format: must be a valid UUID"))

		return
	}

	// Get the session
	a.uploadsLock.RLock()
	session, exists := a.uploads[uploadId]
	a.uploadsLock.RUnlock()

	if !exists {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Msg("upload session not found")
		jsonError(w, http.StatusNotFound, fmt.Errorf("upload session not found: %s", uploadId))

		return
	}

	// Check if session is already being completed/aborted
	if session.completed.Load() {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Msg("upload session is already completing")
		jsonError(w, http.StatusConflict, fmt.Errorf("upload session is already completing or aborted"))

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

	// Calculate offset and expected size for this part
	offset := int64(partNumber) * session.PartSize
	expectedSize := session.PartSize
	if partNumber == session.NumParts-1 {
		// Last part may be smaller
		expectedSize = session.TotalSize - offset
	}

	// Read the part data with size limit
	limitedReader := io.LimitReader(r.Body, expectedSize+1)
	data, err := io.ReadAll(limitedReader)
	if err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error reading part data")
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error reading part data: %w", err))

		return
	}

	size := int64(len(data))

	// Check if part exceeded expected size
	if size > expectedSize {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Int64("size", size).Int64("expectedSize", expectedSize).Msg("part size exceeds expected size")
		jsonError(w, http.StatusBadRequest, fmt.Errorf("part size %d exceeds expected size %d", size, expectedSize))

		return
	}

	// Write directly to the destination file at the correct offset
	// WriteAt is safe for concurrent writes at different offsets, no lock needed here
	_, err = session.DestFile.WriteAt(data, offset)
	if err != nil {
		if errors.Is(err, syscall.ENOSPC) {
			a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("not enough disk space")
			jsonError(w, http.StatusInsufficientStorage, fmt.Errorf("not enough disk space"))

			return
		}
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error writing part data")
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error writing part data: %w", err))

		return
	}

	// Mark part as written - only lock for map access
	session.mu.Lock()
	if session.PartsWritten[partNumber] {
		a.logger.Warn().
			Str(string(logs.OperationIDKey), operationID).
			Str("uploadId", uploadId).
			Int("partNumber", partNumber).
			Msg("overwriting existing part")
	}
	session.PartsWritten[partNumber] = true
	session.mu.Unlock()

	a.logger.Debug().
		Str(string(logs.OperationIDKey), operationID).
		Str("uploadId", uploadId).
		Int("partNumber", partNumber).
		Int64("size", size).
		Int64("offset", offset).
		Msg("part uploaded")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(MultipartUploadPart{
		PartNumber: partNumber,
		Size:       size,
	}); err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("failed to encode response")
	}
}

// PostFilesUploadUploadIdComplete completes a multipart upload
func (a *API) PostFilesUploadUploadIdComplete(w http.ResponseWriter, r *http.Request, uploadId string) {
	defer r.Body.Close()

	operationID := logs.AssignOperationID()

	// Get and remove the session
	a.uploadsLock.Lock()
	session, exists := a.uploads[uploadId]
	if exists {
		// Mark as completed to prevent new parts from being uploaded
		if !session.completed.CompareAndSwap(false, true) {
			// Already being completed by another request
			a.uploadsLock.Unlock()
			a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Msg("upload session is already completing")
			jsonError(w, http.StatusConflict, fmt.Errorf("upload session is already completing"))

			return
		}
		delete(a.uploads, uploadId)
	}
	a.uploadsLock.Unlock()

	if !exists {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Msg("upload session not found")
		jsonError(w, http.StatusNotFound, fmt.Errorf("upload session not found: %s", uploadId))

		return
	}

	// Verify all parts were uploaded
	session.mu.Lock()
	missingParts := []int{}
	for i := range session.NumParts {
		if !session.PartsWritten[i] {
			missingParts = append(missingParts, i)
		}
	}
	session.mu.Unlock()

	if len(missingParts) > 0 {
		session.DestFile.Close()
		os.Remove(session.FilePath)
		a.logger.Error().
			Str(string(logs.OperationIDKey), operationID).
			Str("uploadId", uploadId).
			Ints("missingParts", missingParts).
			Msg("missing parts in upload")
		jsonError(w, http.StatusBadRequest, fmt.Errorf("missing parts: %v", missingParts))

		return
	}

	// Close the file
	if err := session.DestFile.Close(); err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error closing destination file")
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error closing destination file: %w", err))

		return
	}

	a.logger.Debug().
		Str(string(logs.OperationIDKey), operationID).
		Str("uploadId", uploadId).
		Str("filePath", session.FilePath).
		Int64("totalSize", session.TotalSize).
		Int("numParts", session.NumParts).
		Msg("multipart upload completed")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(MultipartUploadComplete{
		Path: session.FilePath,
		Size: session.TotalSize,
	}); err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("failed to encode response")
	}
}

// DeleteFilesUploadUploadId aborts a multipart upload and cleans up
func (a *API) DeleteFilesUploadUploadId(w http.ResponseWriter, r *http.Request, uploadId string) {
	defer r.Body.Close()

	operationID := logs.AssignOperationID()

	// Get and remove the session
	a.uploadsLock.Lock()
	session, exists := a.uploads[uploadId]
	if exists {
		// Mark as completed to prevent new parts from being uploaded
		if !session.completed.CompareAndSwap(false, true) {
			// Already being completed/aborted by another request
			a.uploadsLock.Unlock()
			a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Msg("upload session is already completing")
			jsonError(w, http.StatusConflict, fmt.Errorf("upload session is already completing or aborted"))

			return
		}
		delete(a.uploads, uploadId)
	}
	a.uploadsLock.Unlock()

	if !exists {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Msg("upload session not found")
		jsonError(w, http.StatusNotFound, fmt.Errorf("upload session not found: %s", uploadId))

		return
	}

	// Close and remove the file
	session.DestFile.Close()
	if err := os.Remove(session.FilePath); err != nil && !os.IsNotExist(err) {
		a.logger.Warn().Err(err).Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Msg("error removing file")
	}

	a.logger.Debug().
		Str(string(logs.OperationIDKey), operationID).
		Str("uploadId", uploadId).
		Msg("multipart upload aborted")

	w.WriteHeader(http.StatusNoContent)
}
