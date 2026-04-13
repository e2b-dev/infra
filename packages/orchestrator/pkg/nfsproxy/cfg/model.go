package cfg

import (
	"github.com/willscott/go-nfs"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy/middleware"
)

type Config struct {
	Logging           bool
	Tracing           bool
	Metrics           bool
	RecordStatCalls   bool
	RecordHandleCalls bool
	NFSLogLevel       nfs.LogLevel
	Interceptors      []middleware.Interceptor
}
