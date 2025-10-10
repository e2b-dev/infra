package sandbox

import "github.com/e2b-dev/infra/packages/shared/pkg/smap"

type SandboxesMap struct {
	sandboxes *smap.Map[*Sandbox]
}

func (m *SandboxesMap) Items() map[string]*Sandbox {
	return m.sandboxes.Items()
}

func (m *SandboxesMap) Count() int {
	return m.sandboxes.Count()
}

func (m *SandboxesMap) Get(sandboxID string) (*Sandbox, bool) {
	return m.sandboxes.Get(sandboxID)
}

func (m *SandboxesMap) Insert(sbx *Sandbox) {
	m.sandboxes.Insert(sbx.Runtime.SandboxID, sbx)
}

func (m *SandboxesMap) Remove(sandboxID string) {
	m.sandboxes.Remove(sandboxID)
}

func (m *SandboxesMap) RemoveByExecutionID(sandboxID, executionID string) {
	m.sandboxes.RemoveCb(sandboxID, func(_ string, v *Sandbox, exists bool) bool {
		if !exists {
			return false
		}

		if v == nil {
			return false
		}

		return v.Runtime.ExecutionID == executionID
	})
}

func NewSandboxesMap() *SandboxesMap {
	return &SandboxesMap{sandboxes: smap.New[*Sandbox]()}
}
