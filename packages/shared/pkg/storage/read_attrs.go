package storage

// Closed-enum attribute vocabulary for the orchestrator.read.* /
// orchestrator.chunk.* metric families.

const (
	AttrSource   = "source"
	AttrCodec    = "codec"
	AttrOutcome  = "outcome"
	AttrFileType = "file_type"
)

const (
	OutcomeOK           = "ok"
	OutcomeErrCanceled  = "err_canceled"
	OutcomeErrIO        = "err_io"
	OutcomeErrTimeout   = "err_timeout"
	OutcomeTransitioned = "transitioned"
)
