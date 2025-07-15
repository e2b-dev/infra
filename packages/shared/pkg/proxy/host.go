package proxy

import (
	"strconv"
	"strings"
)

func ParseHost(host string) (sandboxID string, port uint64, err error) {
	dot := strings.Index(host, ".")

	// There must be always domain part used
	if dot == -1 {
		return "", 0, &ErrInvalidHost{}
	}

	// Keep only the left-most subdomain part, i.e. everything before the
	host = host[:dot]

	hostParts := strings.Split(host, "-")
	if len(hostParts) < 2 {
		return "", 0, &ErrInvalidHost{}
	}

	sandboxPortString := hostParts[0]
	sandboxID = hostParts[1]

	sandboxPort, err := strconv.ParseUint(sandboxPortString, 10, 64)
	if err != nil {
		return "", 0, &ErrInvalidSandboxPort{}
	}

	return sandboxID, sandboxPort, nil
}
