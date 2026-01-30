package api

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/user"

	"github.com/e2b-dev/infra/packages/envd/internal/execcontext"
	"github.com/e2b-dev/infra/packages/envd/internal/logs"
	"github.com/e2b-dev/infra/packages/envd/internal/permissions"
)

func (a *API) GetFiles(w http.ResponseWriter, r *http.Request, params GetFilesParams) {
	defer r.Body.Close()

	var errorCode int
	var errMsg error

	var path string
	if params.Path != nil {
		path = *params.Path
	}

	operationID := logs.AssignOperationID()

	// signing authorization if needed
	err := a.validateSigning(r, params.Signature, params.SignatureExpiration, params.Username, path, SigningReadOperation)
	if err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error during auth validation")
		jsonError(w, http.StatusUnauthorized, err)

		return
	}

	username, err := execcontext.ResolveDefaultUsername(params.Username, a.defaults.User)
	if err != nil {
		a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("no user specified")
		jsonError(w, http.StatusBadRequest, err)

		return
	}

	defer func() {
		l := a.logger.
			Err(errMsg).
			Str("method", r.Method+" "+r.URL.Path).
			Str(string(logs.OperationIDKey), operationID).
			Str("path", path).
			Str("username", username)

		if errMsg != nil {
			l = l.Int("error_code", errorCode)
		}

		l.Msg("File read")
	}()

	u, err := user.Lookup(username)
	if err != nil {
		errMsg = fmt.Errorf("error looking up user '%s': %w", username, err)
		errorCode = http.StatusUnauthorized
		jsonError(w, errorCode, errMsg)

		return
	}

	resolvedPath, err := permissions.ExpandAndResolve(path, u, a.defaults.Workdir)
	if err != nil {
		errMsg = fmt.Errorf("error expanding and resolving path '%s': %w", path, err)
		errorCode = http.StatusBadRequest
		jsonError(w, errorCode, errMsg)

		return
	}

	stat, err := os.Stat(resolvedPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			errMsg = fmt.Errorf("path '%s' does not exist", resolvedPath)
			errorCode = http.StatusNotFound
			jsonError(w, errorCode, errMsg)

			return
		}

		errMsg = fmt.Errorf("error checking if path exists '%s': %w", resolvedPath, err)
		errorCode = http.StatusInternalServerError
		jsonError(w, errorCode, errMsg)

		return
	}

	if stat.IsDir() {
		errMsg = fmt.Errorf("path '%s' is a directory", resolvedPath)
		errorCode = http.StatusBadRequest
		jsonError(w, errorCode, errMsg)

		return
	}

	// Validate Accept-Encoding header
	encoding, err := parseAcceptEncoding(r)
	if err != nil {
		errMsg = err
		errorCode = http.StatusBadRequest
		jsonError(w, errorCode, errMsg)

		return
	}

	// Tell caches to store separate variants for different Accept-Encoding values
	w.Header().Set("Vary", "Accept-Encoding")

	// Disable gzip for Range or conditional requests to preserve http.ServeContent behavior
	// (206 Partial Content, 304 Not Modified)
	hasRangeOrConditional := r.Header.Get("Range") != "" ||
		r.Header.Get("If-Modified-Since") != "" ||
		r.Header.Get("If-None-Match") != "" ||
		r.Header.Get("If-Range") != ""
	if hasRangeOrConditional {
		encoding = ""
	}

	file, err := os.Open(resolvedPath)
	if err != nil {
		errMsg = fmt.Errorf("error opening file '%s': %w", resolvedPath, err)
		errorCode = http.StatusInternalServerError
		jsonError(w, errorCode, errMsg)

		return
	}
	defer file.Close()

	// Serve with gzip encoding if requested and no Range/conditional headers.
	// Note: If io.Copy fails after headers are sent, the client receives a truncated
	// gzip stream with HTTP 200. Buffering the entire response would fix this but
	// has memory implications for large files. Clients should validate gzip integrity.
	if encoding == "gzip" {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "application/octet-stream")

		gw := gzip.NewWriter(w)
		defer gw.Close()

		_, err = io.Copy(gw, file)
		if err != nil {
			// Headers already sent, can only log the error. Client will receive truncated response.
			a.logger.Error().Err(err).Str(string(logs.OperationIDKey), operationID).Msg("error writing gzip response")
		}

		return
	}

	http.ServeContent(w, r, path, stat.ModTime(), file)
}
