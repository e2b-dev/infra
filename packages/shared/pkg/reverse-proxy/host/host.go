package host

import (
	"net/url"
	"strconv"
	"strings"

	"go.uber.org/zap"
)

type ErrInvalidHost struct{}

func (e ErrInvalidHost) Error() string {
	return "invalid host to proxy"
}

type ErrInvalidSandboxPort struct{}

func (e ErrInvalidSandboxPort) Error() string {
	return "invalid sandbox port"
}

type ErrSandboxNotFound struct{}

func (e ErrSandboxNotFound) Error() string {
	return "sandbox not found"
}

type SandboxHostContextKey struct{}

type SandboxHost struct {
	Url       *url.URL
	SandboxId string
	Logger    *zap.Logger
}

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
