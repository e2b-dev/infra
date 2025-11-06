package proxy

import (
	"net/http"
	"strconv"
	"strings"
)

func GetHostPort(r *http.Request) (string, uint64, error) {
	sandboxId, port, err := parseHost(r.Host)
	if err != nil {
		sandboxId, port, err = parseHeader(r.Header)
	}
	return sandboxId, port, err
}

func parseHost(host string) (sandboxID string, port uint64, err error) {
	dot := strings.Index(host, ".")

	// There must be always domain part used
	if dot == -1 {
		return "", 0, InvalidHostError{}
	}

	// Keep only the left-most subdomain part, i.e. everything before the
	host = host[:dot]

	hostParts := strings.Split(host, "-")
	if len(hostParts) < 2 {
		return "", 0, InvalidHostError{}
	}

	sandboxPortString := hostParts[0]
	sandboxID = hostParts[1]

	sandboxPort, err := strconv.ParseUint(sandboxPortString, 10, 64)
	if err != nil {
		return "", 0, InvalidSandboxPortError{}
	}

	return sandboxID, sandboxPort, nil
}

const (
	headerSandboxID   = "x-sandbox-id"
	headerSandboxPort = "x-sandbox-port"
)

func parseHeader(h http.Header) (sandboxID string, port uint64, err error) {
	sandboxID = h.Get(headerSandboxID)
	if sandboxID == "" {
		return "", 0, InvalidHostError{}
	}

	portString := h.Get(headerSandboxPort)
	if portString == "" {
		return "", 0, InvalidSandboxPortError{}
	}

	port, err = strconv.ParseUint(portString, 10, 64)
	if err != nil {
		return "", 0, InvalidSandboxPortError{}
	}

	return sandboxID, port, nil
}
