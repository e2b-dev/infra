package telemetry

import (
	"encoding/hex"
	"strings"
)

const (
	GCPTraceContextHeader = "X-Cloud-Trace-Context"
	AWSTraceContextHeader = "X-Amzn-Trace-Id"
)

// ParseEdgeTraceID extracts a trace ID from cloud-provider trace headers.
// These headers are untrusted and are only used for cross-service correlation.
func ParseEdgeTraceID(gcpHeader, awsHeader string) (string, bool) {
	if id, ok := parseGCPTraceID(gcpHeader); ok {
		return id, true
	}

	return parseAWSTraceID(awsHeader)
}

func parseGCPTraceID(header string) (string, bool) {
	if header == "" {
		return "", false
	}

	traceID, _, ok := strings.Cut(header, "/")
	if !ok {
		return "", false
	}

	if !isHexOfLen(traceID, 16) {
		return "", false
	}

	return traceID, true
}

func parseAWSTraceID(header string) (string, bool) {
	if header == "" {
		return "", false
	}

	for field := range strings.SplitSeq(header, ";") {
		key, val, ok := strings.Cut(strings.TrimSpace(field), "=")
		if !ok || key != "Root" {
			continue
		}

		// Root=1-{8hex}-{24hex}
		parts := strings.SplitN(val, "-", 3)
		if len(parts) != 3 || parts[0] != "1" {
			return "", false
		}

		if !isHexOfLen(parts[1], 4) || !isHexOfLen(parts[2], 12) {
			return "", false
		}

		return parts[1] + parts[2], true
	}

	return "", false
}

func isHexOfLen(s string, byteLen int) bool {
	b, err := hex.DecodeString(s)

	return err == nil && len(b) == byteLen
}
