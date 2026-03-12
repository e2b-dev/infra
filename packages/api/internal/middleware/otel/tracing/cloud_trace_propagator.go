package tracing

import "strings"

const cloudTraceContextHeader = "X-Cloud-Trace-Context"

// parseEdgeTraceID extracts just the trace ID from an X-Cloud-Trace-Context header.
// Format: TRACE_ID/SPAN_ID;o=TRACE_TRUE
func parseEdgeTraceID(header string) (string, bool) {
	if header == "" {
		return "", false
	}

	traceID, _, ok := strings.Cut(header, "/")
	if !ok || len(traceID) != 32 {
		return "", false
	}

	return traceID, true
}
