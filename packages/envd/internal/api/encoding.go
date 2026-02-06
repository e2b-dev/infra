package api

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
)

const (
	// EncodingGzip is the gzip content encoding.
	EncodingGzip = "gzip"
	// EncodingIdentity means no encoding (passthrough).
	EncodingIdentity = "identity"
	// EncodingWildcard means any encoding is acceptable.
	EncodingWildcard = "*"
)

// SupportedEncodings lists the content encodings supported for file transfer.
// The order matters - encodings are checked in order of preference.
var SupportedEncodings = []string{
	EncodingGzip,
}

// encodingWithQuality holds an encoding name and its quality value.
type encodingWithQuality struct {
	encoding string
	quality  float64
}

// isSupportedEncoding checks if the given encoding is in the supported list.
// Per RFC 7231, content-coding values are case-insensitive.
func isSupportedEncoding(encoding string) bool {
	return slices.Contains(SupportedEncodings, strings.ToLower(encoding))
}

// parseEncodingWithQuality parses an encoding value and extracts the quality.
// Returns the encoding name (lowercased) and quality value (default 1.0 if not specified).
// Per RFC 7231, content-coding values are case-insensitive.
func parseEncodingWithQuality(value string) encodingWithQuality {
	value = strings.TrimSpace(value)
	quality := 1.0

	if idx := strings.Index(value, ";"); idx != -1 {
		params := value[idx+1:]
		value = strings.TrimSpace(value[:idx])

		// Parse q=X.X parameter
		for param := range strings.SplitSeq(params, ";") {
			param = strings.TrimSpace(param)
			if strings.HasPrefix(strings.ToLower(param), "q=") {
				if q, err := strconv.ParseFloat(param[2:], 64); err == nil {
					quality = q
				}
			}
		}
	}

	// Normalize encoding to lowercase per RFC 7231
	return encodingWithQuality{encoding: strings.ToLower(value), quality: quality}
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
		return EncodingIdentity, nil
	}

	encoding := parseEncoding(header)

	if encoding == EncodingIdentity {
		return EncodingIdentity, nil
	}

	if !isSupportedEncoding(encoding) {
		return "", fmt.Errorf("unsupported Content-Encoding: %s, supported: %v", header, SupportedEncodings)
	}

	return encoding, nil
}

// parseAcceptEncoding parses the Accept-Encoding header and returns the best
// supported encoding based on quality values. Per RFC 7231 section 5.3.4,
// identity is always implicitly acceptable unless explicitly rejected with q=0.
// If no Accept-Encoding header is present, returns empty string (identity).
func parseAcceptEncoding(r *http.Request) (string, error) {
	header := r.Header.Get("Accept-Encoding")
	if header == "" {
		return EncodingIdentity, nil
	}

	// Parse all encodings with their quality values
	var encodings []encodingWithQuality
	for value := range strings.SplitSeq(header, ",") {
		eq := parseEncodingWithQuality(value)
		encodings = append(encodings, eq)
	}

	// Sort by quality value (highest first)
	sort.Slice(encodings, func(i, j int) bool {
		return encodings[i].quality > encodings[j].quality
	})

	// Check if identity is explicitly rejected
	identityRejected := false
	for _, eq := range encodings {
		if eq.encoding == EncodingIdentity && eq.quality == 0 {
			identityRejected = true

			break
		}
	}

	// Find the best supported encoding
	for _, eq := range encodings {
		// Skip encodings with q=0 (explicitly rejected)
		if eq.quality == 0 {
			continue
		}

		if eq.encoding == EncodingIdentity {
			return EncodingIdentity, nil
		}

		// Wildcard means any encoding is acceptable - return a supported encoding if identity is rejected
		if eq.encoding == EncodingWildcard {
			if identityRejected && len(SupportedEncodings) > 0 {
				return SupportedEncodings[0], nil
			}

			return EncodingIdentity, nil
		}

		if isSupportedEncoding(eq.encoding) {
			return eq.encoding, nil
		}
	}

	// Per RFC 7231, identity is implicitly acceptable unless rejected
	if !identityRejected {
		return EncodingIdentity, nil
	}

	// Identity rejected and no supported encodings found
	return "", fmt.Errorf("no acceptable encoding found, supported: %v", SupportedEncodings)
}

// getDecompressedBody returns a reader that decompresses the request body based on
// Content-Encoding header. Returns the original body if no encoding is specified.
// Returns an error if an unsupported encoding is specified.
// The caller is responsible for closing both the returned ReadCloser and the
// original request body (r.Body) separately.
func getDecompressedBody(r *http.Request) (io.ReadCloser, error) {
	encoding, err := parseContentEncoding(r)
	if err != nil {
		return nil, err
	}

	if encoding == EncodingIdentity {
		return r.Body, nil
	}

	switch encoding {
	case EncodingGzip:
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
