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
	// maxNumParts caps the number of parts to prevent memory/CPU exhaustion.
	// With totalSize=10GB and partSize=1, numParts would be ~10 billion without this.
	maxNumParts = 10_000
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

	// Compute numParts as int64 and validate the cap before any file I/O.
	// The cast to int is safe after the cap check (maxNumParts fits in any int).
	var numParts64 int64
	if body.TotalSize > 0 {
		numParts64 = (body.TotalSize + body.PartSize - 1) / body.PartSize
	}

	if numParts64 > maxNumParts {
		jsonError(w, http.StatusBadRequest, fmt.Errorf("upload would require %d parts, exceeding the maximum of %d (increase partSize)", numParts64, maxNumParts))

		return
	}

	numParts := int(numParts64)

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

	// Register a placeholder session under the lock to claim the path and
	// count toward the session limit, then perform file I/O outside the lock
	// to avoid blocking unrelated upload operations. The session starts with
	// completed=true so any concurrent access (Put/Complete/Delete) is safely
	// rejected until initialization finishes.
	uploadID := uuid.NewString()
	tempPath := filePath + ".upload." + uploadID

	session := &multipartUploadSession{
		UploadID:  uploadID,
		FilePath:  filePath,
		TempPath:  tempPath,
		TotalSize: body.TotalSize,
		PartSize:  body.PartSize,
		NumParts:  numParts,
		UID:       uid,
		GID:       gid,
	}
	session.completed.Store(true) // Block access until initialization finishes

	a.uploadsLock.Lock()
	if len(a.uploads) >= maxUploadSessions {
		a.uploadsLock.Unlock()
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Int("maxSessions", maxUploadSessions).Msg("too many concurrent upload sessions")
		jsonError(w, http.StatusTooManyRequests, fmt.Errorf("too many concurrent upload sessions (max %d)", maxUploadSessions))

		return
	}
	for _, existing := range a.uploads {
		if existing.FilePath == filePath {
			a.uploadsLock.Unlock()
			a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("filePath", filePath).Msg("destination path already has an active upload")
			jsonError(w, http.StatusConflict, fmt.Errorf("destination path %q already has an active upload session", filePath))

			return
		}
	}
	a.uploads[uploadID] = session
	a.uploadsLock.Unlock()

	// removeSession unregisters the placeholder on file I/O failure.
	removeSession := func() {
		a.uploadsLock.Lock()
		delete(a.uploads, uploadID)
		a.uploadsLock.Unlock()
	}

	// Ensure parent directories exist after the authoritative session-limit
	// check to avoid creating directories for requests that will be rejected.
	if err := permissions.EnsureDirs(filepath.Dir(filePath), uid, gid); err != nil {
		removeSession()
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error ensuring directories")
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error ensuring directories for %q: %w", filepath.Dir(filePath), err))

		return
	}

	// Create and preallocate a temporary file outside the lock; the final
	// path is untouched until complete atomically renames the temp file
	// into place. The temp path is unique per upload ID so no other
	// operation can conflict.
	destFile, err := os.OpenFile(tempPath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		removeSession()
		if errors.Is(err, syscall.ENOSPC) {
			a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("not enough disk space")
			jsonError(w, http.StatusInsufficientStorage, fmt.Errorf("not enough disk space"))

			return
		}
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error creating temp file")
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error creating temp file: %w", err))

		return
	}

	// Preallocate the file to the total size (creates sparse file)
	if body.TotalSize > 0 {
		if err := destFile.Truncate(body.TotalSize); err != nil {
			destFile.Close()
			if rmErr := ignoreNotExist(os.Remove(tempPath)); rmErr != nil {
				a.logger.Warn().Err(rmErr).Str(string(logs.OperationIDKey), operationID).Msg("failed to remove temp file after truncate error")
			}
			removeSession()
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

	// Set ownership on the temp file
	if err := os.Chown(tempPath, uid, gid); err != nil {
		destFile.Close()
		if rmErr := ignoreNotExist(os.Remove(tempPath)); rmErr != nil {
			a.logger.Warn().Err(rmErr).Str(string(logs.OperationIDKey), operationID).Msg("failed to remove temp file after chown error")
		}
		removeSession()
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error changing file ownership")
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error changing file ownership: %w", err))

		return
	}

	// Initialization complete — set the file handle and parts map under
	// session.mu, then clear the completed flag. The mutex ensures that
	// any goroutine that later acquires session.mu and observes
	// completed==false is guaranteed to see DestFile and Parts.
	session.mu.Lock()
	session.DestFile = destFile
	session.Parts = make(map[int]partStatus)
	session.completed.Store(false)
	session.mu.Unlock()

	a.logger.Debug().
		Str(string(logs.OperationIDKey), operationID).
		Str("uploadId", uploadID).
		Str("filePath", filePath).
		Str("tempPath", tempPath).
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
		// Client never received the uploadId, so clean up to avoid a permanent leak.
		// Set completed under session.mu to synchronize with part uploads that
		// check completed and call wg.Add under the same lock.
		session.mu.Lock()
		session.completed.Store(true)
		session.mu.Unlock()
		removeSession()
		// Wait for any in-flight part writes before closing the file descriptor.
		session.wg.Wait()
		destFile.Close()
		if rmErr := ignoreNotExist(os.Remove(tempPath)); rmErr != nil {
			a.logger.Warn().Err(rmErr).Str(string(logs.OperationIDKey), operationID).Msg("failed to remove temp file after response encoding error")
		}
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

	// Validate part number is non-negative
	if params.Part < 0 {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Int("part", params.Part).Msg("negative part number")
		jsonError(w, http.StatusBadRequest, fmt.Errorf("part number must be non-negative, got %d", params.Part))

		return
	}

	// Reject parts for empty files (no parts expected)
	if session.NumParts == 0 {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Int("partNumber", params.Part).Msg("upload has no parts (empty file)")
		jsonError(w, http.StatusBadRequest, fmt.Errorf("upload has no parts (empty file); no part uploads are accepted"))

		return
	}

	// Check part number is within range
	if params.Part >= session.NumParts {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Int("partNumber", params.Part).Int("numParts", session.NumParts).Msg("part number out of range")
		jsonError(w, http.StatusBadRequest, fmt.Errorf("part number %d out of range (expected 0-%d)", params.Part, session.NumParts-1))

		return
	}

	// Calculate offset and expected size for this part
	offset := int64(params.Part) * session.PartSize
	expectedSize := session.PartSize
	if params.Part == session.NumParts-1 {
		// Last part may be smaller
		expectedSize = session.TotalSize - offset
	}

	// Reserve this part under lock to prevent concurrent writes to the same part number
	// and to authoritatively check completed status (the atomic check above is a fast path).
	// Also register with the WaitGroup so Complete/Delete wait for this write to finish.
	session.mu.Lock()
	if session.completed.Load() {
		session.mu.Unlock()
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Msg("upload session completed during part reservation")
		jsonError(w, http.StatusConflict, fmt.Errorf("upload session %s is already completing or aborted", uploadId))

		return
	}
	if session.Parts[params.Part] == partInProgress {
		session.mu.Unlock()
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Int("partNumber", params.Part).Msg("part is already being uploaded by another request")
		jsonError(w, http.StatusConflict, fmt.Errorf("part %d is already being uploaded by another request for session %s", params.Part, uploadId))

		return
	}
	if session.Parts[params.Part] == partComplete {
		session.mu.Unlock()
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Int("partNumber", params.Part).Msg("part was already uploaded")
		jsonError(w, http.StatusConflict, fmt.Errorf("part %d was already uploaded for session %s", params.Part, uploadId))

		return
	}
	session.Parts[params.Part] = partInProgress
	session.wg.Add(1) // Must happen under mu while completed is false to avoid Add/Wait race
	session.mu.Unlock()

	// Always signal writer completion so Complete/Delete can proceed.
	// This must be the first defer (runs last) so cleanup below finishes first.
	defer session.wg.Done()

	// Ensure in-progress flag is cleaned up on any early return (write errors, size mismatch, etc.)
	partWritten := false
	defer func() {
		if !partWritten {
			session.mu.Lock()
			delete(session.Parts, params.Part)
			session.mu.Unlock()
		}
	}()

	// Limit the request body to expectedSize+1 so the server does not buffer
	// an arbitrarily large oversized body. The +1 allows the trailing-byte
	// check below to detect excess data without triggering MaxBytesError
	// during io.CopyN itself (which reads exactly expectedSize bytes).
	r.Body = http.MaxBytesReader(w, r.Body, expectedSize+1)

	// Stream the part data directly to the file at offset without buffering the
	// entire part in memory. OffsetWriter + CopyN uses a small internal buffer
	// (~32KB) instead of reading the full part into a single allocation.
	offsetWriter := io.NewOffsetWriter(session.DestFile, offset)
	written, err := io.CopyN(offsetWriter, r.Body, expectedSize)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		if errors.Is(err, syscall.ENOSPC) {
			a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("not enough disk space")
			jsonError(w, http.StatusInsufficientStorage, fmt.Errorf("not enough disk space"))

			return
		}
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error writing part data")
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error writing part %d data: %w", params.Part, err))

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

	// Finalize: mark the part as complete since the data was written to disk.
	// Mark partWritten so the deferred cleanup does not revert the status.
	// We intentionally do not check session.completed here — the write
	// succeeded and Complete's parts scan will count it, so returning 200
	// gives the client an accurate view regardless of concurrent completion.
	session.mu.Lock()
	session.Parts[params.Part] = partComplete
	partWritten = true
	session.mu.Unlock()

	a.logger.Debug().
		Str(string(logs.OperationIDKey), operationID).
		Str("uploadId", uploadId).
		Int("partNumber", params.Part).
		Int64("size", written).
		Int64("offset", offset).
		Msg("part uploaded")

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(MultipartUploadPart{
		PartNumber: params.Part,
		Size:       written,
	}); err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("failed to encode response")
	}
}

// PostFilesUploadUploadIdComplete completes a multipart upload
func (a *API) PostFilesUploadUploadIdComplete(w http.ResponseWriter, r *http.Request, uploadId string) {
	defer r.Body.Close()

	operationID := logs.AssignOperationID()

	// Look up the session.
	a.uploadsLock.RLock()
	session, exists := a.uploads[uploadId]
	a.uploadsLock.RUnlock()

	if !exists {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Msg("upload session not found")
		jsonError(w, http.StatusNotFound, fmt.Errorf("upload session not found: %s", uploadId))

		return
	}

	// Mark as completing under session.mu so the transition is synchronized
	// with part reservation (which checks completed and calls wg.Add under
	// the same lock). This prevents a part upload from calling wg.Add(1)
	// after our wg.Wait below has already observed a zero counter.
	session.mu.Lock()
	if !session.completed.CompareAndSwap(false, true) {
		session.mu.Unlock()
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Msg("upload session is already completing")
		jsonError(w, http.StatusConflict, fmt.Errorf("upload session %s is already completing", uploadId))

		return
	}
	session.mu.Unlock()

	// Wait for all in-flight part writes to finish before checking part status.
	// This prevents closing the file while io.CopyN is still writing and ensures
	// parts that were mid-write when completed was set are properly accounted for.
	session.wg.Wait()

	// Verify all parts were uploaded
	session.mu.Lock()
	var missingParts []int
	for i := range session.NumParts {
		if session.Parts[i] != partComplete {
			missingParts = append(missingParts, i)
		}
	}
	session.mu.Unlock()

	if len(missingParts) > 0 {
		// Reset completed flag under session.mu so the client can upload missing
		// parts and retry. Holding the lock prevents a concurrent Complete from
		// winning the CAS (false→true) before this goroutine has returned,
		// which would cause two goroutines to race on Close/Rename.
		session.mu.Lock()
		session.completed.Store(false)
		session.mu.Unlock()
		a.logger.Error().
			Str(string(logs.OperationIDKey), operationID).
			Str("uploadId", uploadId).
			Int("missingCount", len(missingParts)).
			Msg("missing parts in upload")
		jsonError(w, http.StatusBadRequest, fmt.Errorf("missing %d of %d parts", len(missingParts), session.NumParts))

		return
	}

	// All parts present — close the file and rename to the final path.
	// The session stays in the map during finalization to prevent a new
	// upload to the same path from starting before the rename completes.
	if err := session.DestFile.Close(); err != nil {
		a.uploadsLock.Lock()
		delete(a.uploads, uploadId)
		a.uploadsLock.Unlock()
		if rmErr := ignoreNotExist(os.Remove(session.TempPath)); rmErr != nil {
			a.logger.Warn().Err(rmErr).Str(string(logs.OperationIDKey), operationID).Msg("failed to remove temp file after close error")
		}
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error closing temp file")
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error closing temp file: %w", err))

		return
	}

	if err := os.Rename(session.TempPath, session.FilePath); err != nil {
		a.uploadsLock.Lock()
		delete(a.uploads, uploadId)
		a.uploadsLock.Unlock()
		if rmErr := ignoreNotExist(os.Remove(session.TempPath)); rmErr != nil {
			a.logger.Warn().Err(rmErr).Str(string(logs.OperationIDKey), operationID).Msg("failed to remove temp file after rename error")
		}
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error renaming temp file to destination")
		jsonError(w, http.StatusInternalServerError, fmt.Errorf("error renaming temp file to destination: %w", err))

		return
	}

	a.uploadsLock.Lock()
	delete(a.uploads, uploadId)
	a.uploadsLock.Unlock()

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

	// Look up the session under a read lock, then operate on it
	// independently. This avoids holding the global write lock during
	// the CAS, which would block all concurrent RLock callers.
	a.uploadsLock.RLock()
	session, exists := a.uploads[uploadId]
	a.uploadsLock.RUnlock()

	if !exists {
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Msg("upload session not found")
		jsonError(w, http.StatusNotFound, fmt.Errorf("upload session not found: %s", uploadId))

		return
	}

	// Mark as completed under session.mu to synchronize with part
	// reservation (which checks completed and calls wg.Add under the
	// same lock). This prevents a part upload from calling wg.Add(1)
	// after our wg.Wait below has already observed a zero counter.
	session.mu.Lock()
	if !session.completed.CompareAndSwap(false, true) {
		session.mu.Unlock()
		a.logger.Error().Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Msg("upload session is already completing")
		jsonError(w, http.StatusConflict, fmt.Errorf("upload session %s is already completing or aborted", uploadId))

		return
	}
	session.mu.Unlock()

	// Remove session from map under the write lock.
	a.uploadsLock.Lock()
	delete(a.uploads, uploadId)
	a.uploadsLock.Unlock()

	// Unlink the temp file. The temp path is unique per upload ID so no
	// other operation can conflict. In-flight writers use the open DestFile
	// descriptor, which remains valid after unlink.
	if err := ignoreNotExist(os.Remove(session.TempPath)); err != nil {
		a.logger.Warn().Err(err).Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Msg("error removing temp file")
	}

	// Wait for any in-flight part writes to finish before closing the file descriptor
	session.wg.Wait()
	if err := session.DestFile.Close(); err != nil {
		a.logger.Warn().Err(err).Str(string(logs.OperationIDKey), operationID).Str("uploadId", uploadId).Msg("error closing temp file during abort")
	}

	a.logger.Debug().
		Str(string(logs.OperationIDKey), operationID).
		Str("uploadId", uploadId).
		Msg("multipart upload aborted")

	w.WriteHeader(http.StatusNoContent)
}
