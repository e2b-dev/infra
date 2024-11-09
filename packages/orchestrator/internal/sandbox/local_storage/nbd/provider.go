package nbd

import (
	"io"
)

type Provider interface {
	io.ReaderAt
	io.WriterAt
	Size() (int64, error)
}

type LockableProvider interface {
	Provider
	Lock()
	Unlock()
}

type ExposedStorage interface {
	Init() error
	Shutdown() error
	Device() string
	SetProvider(prov Provider)
}

type BlockOrder interface {
	AddAll()
	Add(block int)
	Remove(block int)
	GetNext() *BlockInfo
}

type BlockInfo struct {
	Block int
	Type  int
}
