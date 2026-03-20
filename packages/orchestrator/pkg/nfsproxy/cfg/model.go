package cfg

import "github.com/willscott/go-nfs"

type Config struct {
	Logging           bool
	Tracing           bool
	RecordStatCalls   bool
	RecordHandleCalls bool
	NFSLogLevel       nfs.LogLevel
}
