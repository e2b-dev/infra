package api

import (
	"errors"
	"fmt"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	"net/http"
	"slices"
	"strconv"
	"time"
)

const (
	SigningReadOperation  = "read"
	SigningWriteOperation = "write"

	accessTokenHeader = "X-Access-Token"
)

var (
	// paths that are always allowed without general authentication
	allowedPaths = []string{
		"GET/health",
		"GET/files",
		"POST/files",
	}
)

func (a *API) WithAuthorization(handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if a.accessToken != nil {
			authHeader := req.Header.Get(accessTokenHeader)

			// check if this path is allowed without authentication (e.g., health check, endpoints supporting signing)
			allowedPath := slices.Contains(allowedPaths, req.Method+req.URL.Path)

			if authHeader != *a.accessToken && !allowedPath {
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
	if a.accessToken == nil {
		return "", fmt.Errorf("access token is not set")
	}

	var signature string
	hasher := keys.NewSHA256Hashing()

	if signatureExpiration == nil {
		signature = fmt.Sprintf("%s:%s:%s:%s", path, operation, username, *a.accessToken)
	} else {
		signature = fmt.Sprintf("%s:%s:%s:%s:%s", path, operation, username, *a.accessToken, strconv.FormatInt(*signatureExpiration, 10))
	}

	return fmt.Sprintf("v1_%s", hasher.HashWithoutPrefix([]byte(signature))), nil
}

func (a *API) validateSigning(r *http.Request, signature *string, signatureExpiration *int, username string, path string, operation string) (err error) {
	var expectedSignature string

	// no need to validate signing key if access token is not set
	if a.accessToken == nil {
		return nil
	}

	// check if access token is sent in the header
	tokenFromHeader := r.Header.Get(accessTokenHeader)
	if tokenFromHeader != "" && tokenFromHeader != *a.accessToken {
		return fmt.Errorf("access token present in header but does not match")
	}

	if signature == nil {
		return fmt.Errorf("missing signature query parameter")
	}

	if signatureExpiration == nil {
		expectedSignature, err = a.generateSignature(path, username, operation, nil)
	} else {
		exp := int64(*signatureExpiration)
		expectedSignature, err = a.generateSignature(path, username, operation, &exp)
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
