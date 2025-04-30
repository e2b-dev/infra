package routing

import (
	"strconv"
	"strings"
)

func ParseHost(host string) (sandboxID string, port uint64, err error) {
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
