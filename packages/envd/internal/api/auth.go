package api

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/awnumar/memguard"

	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
)

const (
	SigningReadOperation  = "read"
	SigningWriteOperation = "write"

	accessTokenHeader = "X-Access-Token"
)

// paths that are always allowed without general authentication
// POST/init is secured via MMDS hash validation instead
var authExcludedPaths = []string{
	"GET/health",
	"GET/files",
	"POST/files",
	"POST/init",
}

func (a *API) WithAuthorization(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if a.accessToken.IsSet() {
			authHeader := req.Header.Get(accessTokenHeader)

			// check if this path is allowed without authentication (e.g., health check, endpoints supporting signing)
			allowedPath := slices.Contains(authExcludedPaths, req.Method+req.URL.Path)

			if !a.accessToken.Equals(authHeader) && !allowedPath {
				a.logger.Error().Msg("Trying to access secured envd without correct access token")

				err := fmt.Errorf("unauthorized access, please provide a valid access token or method signing if supported")
				jsonError(w, http.StatusUnauthorized, err)

				return
			}
		}

		handler.ServeHTTP(w, req)
	})
}

func (a *API) generateSignature(path string, username string, operation string, signatureExpiration *int64) (string, error) {
	tokenBytes, err := a.accessToken.Bytes()
	if err != nil {
		return "", fmt.Errorf("access token is not set: %w", err)
	}
	defer memguard.WipeBytes(tokenBytes)

	var signature string
	hasher := keys.NewSHA256Hashing()

	if signatureExpiration == nil {
		signature = strings.Join([]string{path, operation, username, string(tokenBytes)}, ":")
	} else {
		signature = strings.Join([]string{path, operation, username, string(tokenBytes), strconv.FormatInt(*signatureExpiration, 10)}, ":")
	}

	return fmt.Sprintf("v1_%s", hasher.HashWithoutPrefix([]byte(signature))), nil
}

func (a *API) validateSigning(r *http.Request, signature *string, signatureExpiration *int, username *string, path string, operation string) (err error) {
	var expectedSignature string

	// no need to validate signing key if access token is not set
	if !a.accessToken.IsSet() {
		return nil
	}

	// check if access token is sent in the header
	tokenFromHeader := r.Header.Get(accessTokenHeader)
	if tokenFromHeader != "" {
		if !a.accessToken.Equals(tokenFromHeader) {
			return fmt.Errorf("access token present in header but does not match")
		}

		return nil
	}

	if signature == nil {
		return fmt.Errorf("missing signature query parameter")
	}

	// Empty string is used when no username is provided and the default user should be used
	signatureUsername := ""
	if username != nil {
		signatureUsername = *username
	}

	if signatureExpiration == nil {
		expectedSignature, err = a.generateSignature(path, signatureUsername, operation, nil)
	} else {
		exp := int64(*signatureExpiration)
		expectedSignature, err = a.generateSignature(path, signatureUsername, operation, &exp)
	}

	if err != nil {
		a.logger.Error().Err(err).Msg("error generating signing key")

		return errors.New("invalid signature")
	}

	// signature validation
	if expectedSignature != *signature {
		return fmt.Errorf("invalid signature")
	}

	// signature expiration
	if signatureExpiration != nil {
		exp := int64(*signatureExpiration)
		if exp < time.Now().Unix() {
			return fmt.Errorf("signature is already expired")
		}
	}

	return nil
}
