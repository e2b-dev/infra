package api

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// SupportedEncodings lists the content encodings supported for file transfer.
// The order matters - encodings are checked in order of preference.
var SupportedEncodings = []string{
	"gzip",
}

// encodingWithQuality holds an encoding name and its quality value.
type encodingWithQuality struct {
	encoding string
	quality  float64
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

// parseEncodingWithQuality parses an encoding value and extracts the quality.
// Returns the encoding name and quality value (default 1.0 if not specified).
func parseEncodingWithQuality(value string) encodingWithQuality {
	value = strings.TrimSpace(value)
	quality := 1.0

	if idx := strings.Index(value, ";"); idx != -1 {
		params := value[idx+1:]
		value = strings.TrimSpace(value[:idx])

		// Parse q=X.X parameter
		for _, param := range strings.Split(params, ";") {
			param = strings.TrimSpace(param)
			if strings.HasPrefix(param, "q=") {
				if q, err := strconv.ParseFloat(param[2:], 64); err == nil {
					quality = q
				}
			}
		}
	}

	return encodingWithQuality{encoding: value, quality: quality}
}

// parseEncoding extracts the encoding name from a header value, stripping quality.
func parseEncoding(value string) string {
	return parseEncodingWithQuality(value).encoding
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
// encoding based on quality values. Returns an error if only unsupported encodings
// are requested. If no Accept-Encoding header is present, returns empty string.
func parseAcceptEncoding(r *http.Request) (string, error) {
	header := r.Header.Get("Accept-Encoding")
	if header == "" {
		return "", nil
	}

	// Parse all encodings with their quality values
	var encodings []encodingWithQuality
	for _, value := range strings.Split(header, ",") {
		eq := parseEncodingWithQuality(value)
		encodings = append(encodings, eq)
	}

	// Sort by quality value (highest first)
	sort.Slice(encodings, func(i, j int) bool {
		return encodings[i].quality > encodings[j].quality
	})

	// Find the best supported encoding
	for _, eq := range encodings {
		// Skip encodings with q=0 (explicitly rejected)
		if eq.quality == 0 {
			continue
		}

		// "identity" means no encoding, "*" means any encoding is acceptable
		if eq.encoding == "identity" || eq.encoding == "*" {
			return "", nil
		}

		if isSupportedEncoding(eq.encoding) {
			return eq.encoding, nil
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
