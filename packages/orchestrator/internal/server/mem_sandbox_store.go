package server

import (
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

type MemSandboxStore struct {
	server    *server
	sandboxes *smap.Map[*sandbox.Sandbox]
}

func NewMemSandboxStore(server *server) *MemSandboxStore {
	return &MemSandboxStore{
		server:    server,
		sandboxes: smap.New[*sandbox.Sandbox](),
	}
}

func (s *MemSandboxStore) Get(id string) (*sandbox.Sandbox, bool) {
	sbx, ok := s.sandboxes.Get(id)
	if !ok {
		return nil, false
	}
	sbx = sbx.WithExternalLogger(s.server.externalLogger)
	sbx = sbx.WithInternalLogger(s.server.internalLogger)
	return sbx, true
}

func (s *MemSandboxStore) Remove(id string) {
	s.sandboxes.Remove(id)
}

func (s *MemSandboxStore) Insert(id string, sbx *sandbox.Sandbox) {
	s.sandboxes.Insert(id, sbx)
}

func (s *MemSandboxStore) Items() map[string]*sandbox.Sandbox {
	return s.sandboxes.Items()
}

func (s *MemSandboxStore) Count() int {
	return s.sandboxes.Count()
}

func (s *MemSandboxStore) InsertIfAbsent(id string, sbx *sandbox.Sandbox) bool {
	return s.sandboxes.InsertIfAbsent(id, sbx)
}
