package proxy

import (
	"net/http"
	"strconv"
	"strings"
)

func GetHostPort(r *http.Request) (string, uint64, error) {
	sandboxId, port, ok := parseHost(r.Host)
	if ok {
		return sandboxId, port, nil
	}

	sandboxId, port, ok = parseHeader(r.Header)
	if ok {
		return sandboxId, port, nil
	}

	return "", 0, InvalidHostError{}
}

func parseHost(host string) (sandboxID string, port uint64, ok bool) {
	dot := strings.Index(host, ".")

	// There must be always domain part used
	if dot == -1 {
		return "", 0, false
	}

	// Keep only the left-most subdomain part, i.e. everything before the
	host = host[:dot]

	hostParts := strings.Split(host, "-")
	if len(hostParts) < 2 {
		return "", 0, false
	}

	sandboxPortString := hostParts[0]
	sandboxID = hostParts[1]

	sandboxPort, err := strconv.ParseUint(sandboxPortString, 10, 64)
	if err != nil {
		return "", 0, false
	}

	return sandboxID, sandboxPort, true
}

const (
	headerSandboxID   = "X-Sandbox-Id"
	headerSandboxPort = "X-Sandbox-Port"
)

func parseHeader(h http.Header) (sandboxID string, port uint64, ok bool) {
	sandboxID = h.Get(headerSandboxID)
	if sandboxID == "" {
		return "", 0, false
	}

	portString := h.Get(headerSandboxPort)
	if portString == "" {
		return "", 0, false
	}

	port, err := strconv.ParseUint(portString, 10, 64)
	if err != nil {
		return "", 0, false
	}

	return sandboxID, port, true
}
