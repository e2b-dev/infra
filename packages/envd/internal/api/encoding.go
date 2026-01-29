package api

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// SupportedEncodings lists the content encodings supported for file transfer.
// The order matters - encodings are checked in order of preference.
var SupportedEncodings = []string{
	"gzip",
}

// isSupportedEncoding checks if the given encoding is in the supported list.
func isSupportedEncoding(encoding string) bool {
	for _, supported := range SupportedEncodings {
		if encoding == supported {
			return true
		}
	}
	return false
}

// parseEncoding extracts and validates the encoding from a header value.
// Returns the encoding name stripped of any quality value.
func parseEncoding(value string) string {
	encoding := strings.TrimSpace(value)
	// Strip quality value if present (e.g., "gzip;q=1.0" -> "gzip")
	if idx := strings.Index(encoding, ";"); idx != -1 {
		encoding = strings.TrimSpace(encoding[:idx])
	}
	return encoding
}

// parseContentEncoding parses the Content-Encoding header and returns the encoding.
// Returns an error if an unsupported encoding is specified.
// If no Content-Encoding header is present, returns empty string.
func parseContentEncoding(r *http.Request) (string, error) {
	header := r.Header.Get("Content-Encoding")
	if header == "" {
		return "", nil
	}

	encoding := parseEncoding(header)

	// "identity" means no encoding
	if encoding == "identity" {
		return "", nil
	}

	if !isSupportedEncoding(encoding) {
		return "", fmt.Errorf("unsupported Content-Encoding: %s, supported: %v", header, SupportedEncodings)
	}

	return encoding, nil
}

// parseAcceptEncoding parses the Accept-Encoding header and returns the requested
// encoding. Returns an error if only unsupported encodings are requested.
// If no Accept-Encoding header is present, returns empty string (no compression).
func parseAcceptEncoding(r *http.Request) (string, error) {
	header := r.Header.Get("Accept-Encoding")
	if header == "" {
		return "", nil
	}

	for _, value := range strings.Split(header, ",") {
		encoding := parseEncoding(value)

		// "identity" means no encoding, "*" means any encoding is acceptable
		if encoding == "identity" || encoding == "*" {
			return "", nil
		}

		if isSupportedEncoding(encoding) {
			return encoding, nil
		}
	}

	return "", fmt.Errorf("unsupported Accept-Encoding: %s, supported: %v", header, SupportedEncodings)
}

// getDecompressedBody returns a reader that decompresses the request body based on
// Content-Encoding header. Returns the original body if no encoding is specified.
// Returns an error if an unsupported encoding is specified.
// The caller is responsible for closing the returned ReadCloser.
func getDecompressedBody(r *http.Request) (io.ReadCloser, error) {
	encoding, err := parseContentEncoding(r)
	if err != nil {
		return nil, err
	}

	if encoding == "" {
		return r.Body, nil
	}

	switch encoding {
	case "gzip":
		gzReader, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to create gzip reader: %w", err)
		}
		return gzReader, nil
	default:
		// This shouldn't happen if isSupportedEncoding is correct
		return nil, fmt.Errorf("encoding %s is supported but not implemented", encoding)
	}
}
