package storage

// PeerTransitionedError is returned by the peer FramedFile when the GCS upload
// has completed and serialized V4 headers are available. The caller (build.File)
// should atomically swap its header and retry the read — the new header's
// FrameTables will route reads to the correct (possibly compressed) GCS objects.
type PeerTransitionedError struct {
	MemfileHeader []byte
	RootfsHeader  []byte
}

func (e *PeerTransitionedError) Error() string {
	return "peer upload completed, headers available"
}
