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
	// maxTotalSize limits the total upload size to 10GB
	maxTotalSize = 10 * 1024 * 1024 * 1024
	// maxPartSize limits individual part size to 100MB to prevent DoS
	maxPartSize = 100 * 1024 * 1024
	// uploadSessionTTL is the maximum time an upload session can remain active
	uploadSessionTTL = 1 * time.Hour
	// uploadSessionCleanupInterval is how often to check for expired sessions
	uploadSessionCleanupInterval = 5 * time.Minute
	// maxNumParts caps the number of parts to prevent memory/CPU exhaustion.
	// With totalSize=10GB and partSize=1, numParts would be ~10 billion without this.
	maxNumParts = 10_000
	// maxMissingPartsInError caps the number of missing part numbers shown in error responses
	// to avoid huge JSON payloads (e.g. 10,000 missing parts serialized as integers).
	maxMissingPartsInError = 20
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

	if body.PartSize < 1 {
		jsonError(w, http.StatusBadRequest, fmt.Errorf("partSize must be at least 1"))

		return
	}
	if body.TotalSize < 0 {
		jsonError(w, http.StatusBadRequest, fmt.Errorf("totalSize must be non-negative"))

		return
	}
	if body.TotalSize > maxTotalSize {
		jsonError(w, http.StatusBadRequest, fmt.Errorf("totalSize %d exceeds maximum allowed size of %d bytes", body.TotalSize, maxTotalSize))

		return
	}
	if body.PartSize > maxPartSize {
		jsonError(w, http.StatusBadRequest, fmt.Errorf("partSize exceeds maximum allowed size of %d bytes", maxPartSize))

		return
	}

	// Compute numParts and validate the cap before any file I/O.
	var numParts uint
	if body.TotalSize > 0 {
		numParts = uint((body.TotalSize + body.PartSize - 1) / body.PartSize)
	}

	if numParts > maxNumParts {
		jsonError(w, http.StatusBadRequest, fmt.Errorf("upload would require %d parts, exceeding the maximum of %d (increase partSize)", numParts, maxNumParts))

		return
	}

	// Check session limit early, before any file I/O, to avoid truncating
	// existing files only to reject the request due to capacity.
	a.uploadsLock.RLock()
	sessionCount := len(a.uploads)
	a.uploadsLock.RUnlock()

	if sessionCount >= maxUploadSessions {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Int("maxSessions", maxUploadSessions).Msg("too many concurrent upload sessions")
		jsonError(w, http.StatusTooManyRequests, fmt.Errorf("too many concurrent upload sessions (max %d)", maxUploadSessions))

		return
	}

	// Resolve username
	username, err := execcontext.ResolveDefaultUsername(params.Username, a.defaults.User)
	if err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("no user specified")
		jsonError(w, http.StatusBadRequest, fmt.Errorf("error resolving username (provided=%v, default=%q): %w", params.Username, a.defaults.User, err))

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
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error getting user ids for user %q (uid=%s, gid=%s): %w", u.Username, u.Uid, u.Gid, err))

		return
	}

	// Resolve the file path
	filePath, err := permissions.ExpandAndResolve(body.Path, u, a.defaults.Workdir)
	if err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error resolving path")
		jsonError(w, http.StatusBadRequest, fmt.Errorf("error resolving path %q: %w", body.Path, err))

		return
	}

	// Ensure parent directories exist
	if err := permissions.EnsureDirs(filepath.Dir(filePath), uid, gid); err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error ensuring directories")
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error ensuring directories for %q: %w", filepath.Dir(filePath), err))

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

	uploadID := uuid.NewString()

	session := &MultipartUploadSession{
		UploadID:     uploadID,
		FilePath:     filePath,
		DestFile:     destFile,
		TotalSize:    body.TotalSize,
		PartSize:     body.PartSize,
		NumParts:     numParts,
		UID:          uid,
		GID:          gid,
		PartsWritten:    make(map[uint]bool),
		partsInProgress: make(map[uint]bool),
		CreatedAt:       time.Now(),
	}

	// Atomically check session limit and insert — prevents TOCTOU race where
	// concurrent requests all pass a read-lock check before any inserts.
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
		Uint("numParts", numParts).
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

	// Get the session
	a.uploadsLock.RLock()
	session, exists := a.uploads[uploadId]
	a.uploadsLock.RUnlock()

	if !exists {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Msg("upload session not found")
		jsonError(w, http.StatusNotFound, fmt.Errorf("upload session not found: %s", uploadId))

		return
	}

	// Fast-path: reject early if session is already completing (authoritative check under session.mu below)
	if session.completed.Load() {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Msg("upload session is already completing")
		jsonError(w, http.StatusConflict, fmt.Errorf("upload session %s is already completing or aborted", uploadId))

		return
	}

	partNumber := uint(params.Part)

	// Reject parts for empty files (no parts expected)
	if session.NumParts == 0 {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Uint("partNumber", partNumber).Msg("upload has no parts (empty file)")
		jsonError(w, http.StatusBadRequest, fmt.Errorf("upload has no parts (empty file); no part uploads are accepted"))

		return
	}

	// Check part number is within range
	if partNumber >= session.NumParts {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Uint("partNumber", partNumber).Uint("numParts", session.NumParts).Msg("part number out of range")
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

	// Reserve this part under lock to prevent concurrent writes to the same part number
	// and to authoritatively check completed status (the atomic check above is a fast path).
	session.mu.Lock()
	if session.completed.Load() {
		session.mu.Unlock()
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Msg("upload session completed during part reservation")
		jsonError(w, http.StatusConflict, fmt.Errorf("upload session %s is already completing or aborted", uploadId))

		return
	}
	if session.partsInProgress[partNumber] {
		session.mu.Unlock()
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Uint("partNumber", partNumber).Msg("part is already being uploaded by another request")
		jsonError(w, http.StatusConflict, fmt.Errorf("part %d is already being uploaded by another request for session %s", partNumber, uploadId))

		return
	}
	session.partsInProgress[partNumber] = true
	session.mu.Unlock()

	// Ensure in-progress flag is cleaned up on any early return (write errors, size mismatch, etc.)
	partReserved := true
	defer func() {
		if partReserved {
			session.mu.Lock()
			delete(session.partsInProgress, partNumber)
			session.mu.Unlock()
		}
	}()

	// Stream the part data directly to the file at offset without buffering the
	// entire part in memory. OffsetWriter + CopyN uses a small internal buffer
	// (~32KB) instead of reading the full part into a single allocation.
	offsetWriter := io.NewOffsetWriter(session.DestFile, offset)
	written, err := io.CopyN(offsetWriter, r.Body, expectedSize)
	if err != nil && !errors.Is(err, io.EOF) {
		if errors.Is(err, syscall.ENOSPC) {
			a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("not enough disk space")
			jsonError(w, http.StatusInsufficientStorage, fmt.Errorf("not enough disk space"))

			return
		}
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error writing part data")
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error writing part %d data: %w", partNumber, err))

		return
	}

	if written != expectedSize {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Int64("written", written).Int64("expectedSize", expectedSize).Msg("part size mismatch")
		jsonError(w, http.StatusBadRequest, fmt.Errorf("part size %d does not match expected size %d", written, expectedSize))

		return
	}

	// Check for extra data beyond expected size
	var extra [1]byte
	if n, _ := r.Body.Read(extra[:]); n > 0 {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Int64("expectedSize", expectedSize).Msg("part data exceeds expected size")
		jsonError(w, http.StatusBadRequest, fmt.Errorf("part data exceeds expected size %d", expectedSize))

		return
	}

	size := written

	// Finalize: mark part as written under lock. Re-check completed to prevent
	// the race where Complete deletes the file between our write and this point,
	// which would cause us to return 200 while the file is gone.
	session.mu.Lock()
	delete(session.partsInProgress, partNumber)
	partReserved = false
	if session.completed.Load() {
		session.mu.Unlock()
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Uint("partNumber", partNumber).Msg("session completed during part upload")
		jsonError(w, http.StatusConflict, fmt.Errorf("upload session %s was completed or aborted during part upload", uploadId))

		return
	}
	if session.PartsWritten[partNumber] {
		a.logger.Warn().
			Str(string(logs.OperationIDKey), operationID).
			Str("uploadId", uploadId).
			Uint("partNumber", partNumber).
			Msg("overwriting existing part")
	}
	session.PartsWritten[partNumber] = true
	session.mu.Unlock()

	a.logger.Debug().
		Str(string(logs.OperationIDKey), operationID).
		Str("uploadId", uploadId).
		Uint("partNumber", partNumber).
		Int64("size", size).
		Int64("offset", offset).
		Msg("part uploaded")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(MultipartUploadPart{
		PartNumber: int(partNumber),
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
			jsonError(w, http.StatusConflict, fmt.Errorf("upload session %s is already completing", uploadId))

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
	var missingParts []uint
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
			Int("missingCount", len(missingParts)).
			Msg("missing parts in upload")
		// Cap the error message to avoid huge JSON responses (e.g. 10,000 missing parts)
		if len(missingParts) > maxMissingPartsInError {
			jsonError(w, http.StatusBadRequest, fmt.Errorf("missing %d parts (first %d: %v)", len(missingParts), maxMissingPartsInError, missingParts[:maxMissingPartsInError]))
		} else {
			jsonError(w, http.StatusBadRequest, fmt.Errorf("missing parts: %v", missingParts))
		}

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
		Uint("numParts", session.NumParts).
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
			jsonError(w, http.StatusConflict, fmt.Errorf("upload session %s is already completing or aborted", uploadId))

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
	if err := ignoreNotExist(os.Remove(session.FilePath)); err != nil {
		a.logger.Warn().Err(err).Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Msg("error removing file")
	}

	a.logger.Debug().
		Str(string(logs.OperationIDKey), operationID).
		Str("uploadId", uploadId).
		Msg("multipart upload aborted")

	w.WriteHeader(http.StatusNoContent)
}
