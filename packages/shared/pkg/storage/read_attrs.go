package storage

// Closed-enum attribute vocabulary for the orchestrator.read.* /
// orchestrator.chunk.* metric families.

const (
	AttrSource   = "source"
	AttrCodec    = "codec"
	AttrOutcome  = "outcome"
	AttrEvent    = "event"
	AttrFileType = "file_type"
)

const (
	OutcomeOK          = "ok"
	OutcomeErrCanceled = "err_canceled"
	OutcomeErrIO       = "err_io"
	OutcomeErrTimeout  = "err_timeout"
)

const (
	CacheEventHit          = "hit"
	CacheEventMiss         = "miss"
	CacheEventWritebackOK  = "writeback_ok"
	CacheEventWritebackErr = "writeback_err"
)
