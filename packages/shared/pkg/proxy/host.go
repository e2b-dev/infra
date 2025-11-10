package proxy

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func GetTargetFromRequest(processHeaders bool) func(r *http.Request) (sandboxId string, port uint64, err error) {
	return func(r *http.Request) (sandboxId string, port uint64, err error) {
		if processHeaders {
			var ok bool
			sandboxId, port, ok, err = parseHeaders(r.Header)
			if err != nil {
				return "", 0, err
			} else if ok {
				return sandboxId, port, nil
			}
		}

		sandboxId, port, err = parseHost(r.Host)
		if err != nil {
			return "", 0, err
		}

		return sandboxId, port, nil
	}
}

func parseHost(host string) (sandboxID string, port uint64, err error) {
	dot := strings.Index(host, ".")

	// There must be always domain part used
	if dot == -1 {
		return "", 0, ErrInvalidHost
	}

	// Keep only the left-most subdomain part, i.e. everything before the
	host = host[:dot]

	hostParts := strings.Split(host, "-")
	if len(hostParts) < 2 {
		return "", 0, ErrInvalidHost
	}

	sandboxPortString := hostParts[0]
	sandboxID = hostParts[1]

	sandboxPort, err := strconv.ParseUint(sandboxPortString, 10, 64)
	if err != nil {
		return "", 0, InvalidSandboxPortError{sandboxPortString, err}
	}

	return sandboxID, sandboxPort, nil
}

type MissingHeaderError struct {
	Header string
}

func (e MissingHeaderError) Error() string {
	return fmt.Sprintf("Missing header: %s", e.Header)
}

const (
	headerSandboxID   = "E2b-Sandbox-Id"
	headerSandboxPort = "E2b-Sandbox-Port"
)

func parseHeaders(h http.Header) (sandboxID string, port uint64, ok bool, err error) {
	sandboxID = h.Get(headerSandboxID)
	portString := h.Get(headerSandboxPort)

	if sandboxID == "" && portString == "" {
		return "", 0, false, nil
	}

	if sandboxID == "" {
		return "", 0, false, MissingHeaderError{Header: headerSandboxID}
	}

	if portString == "" {
		return "", 0, false, MissingHeaderError{Header: headerSandboxPort}
	}

	port, err = strconv.ParseUint(portString, 10, 64)
	if err != nil {
		return "", 0, false, InvalidSandboxPortError{portString, err}
	}

	return sandboxID, port, true, nil
}
