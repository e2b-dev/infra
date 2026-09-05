package cfg

import "github.com/willscott/go-nfs"

type Config struct {
	Logging           bool
	Tracing           bool
	Metrics           bool
	RecordStatCalls   bool
	RecordHandleCalls bool
	NFSLogLevel       nfs.LogLevel
	// CacheLimit is the LRU capacity for NFS file-handle mappings (uuid → path).
	// When the cache is full the oldest entry is evicted; subsequent RPCs using
	// that handle return ESTALE. Set via NFS_PROXY_CACHE_LIMIT env var.
	CacheLimit int
}
