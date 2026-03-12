package tracing

import (
	"encoding/hex"
	"strings"
)

const cloudTraceContextHeader = "X-Cloud-Trace-Context"

// parseEdgeTraceID extracts just the trace ID from an X-Cloud-Trace-Context header.
// Format: TRACE_ID/SPAN_ID;o=TRACE_TRUE
func parseEdgeTraceID(header string) (string, bool) {
	if header == "" {
		return "", false
	}

	traceID, _, ok := strings.Cut(header, "/")
	if !ok {
		return "", false
	}

	b, err := hex.DecodeString(traceID)
	if err != nil || len(b) != 16 {
		return "", false
	}

	return traceID, true
}
