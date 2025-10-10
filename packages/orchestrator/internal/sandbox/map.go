package sandbox

import "github.com/e2b-dev/infra/packages/shared/pkg/smap"

type MapSubscriber interface {
	OnInsert(sandbox *Sandbox)
	OnRemove(sandboxID string)
}

type Map struct {
	sandboxes   *smap.Map[*Sandbox]
	subscribers []MapSubscriber
}

func (m *Map) Subscribe(subscriber MapSubscriber) {
	m.subscribers = append(m.subscribers, subscriber)
}

func (m *Map) trigger(fn func(MapSubscriber)) {
	for _, subscriber := range m.subscribers {
		fn(subscriber)
	}
}

func (m *Map) Items() map[string]*Sandbox {
	return m.sandboxes.Items()
}

func (m *Map) Count() int {
	return m.sandboxes.Count()
}

func (m *Map) Get(sandboxID string) (*Sandbox, bool) {
	return m.sandboxes.Get(sandboxID)
}

func (m *Map) Insert(sbx *Sandbox) {
	m.sandboxes.Insert(sbx.Runtime.SandboxID, sbx)

	go m.trigger(func(s MapSubscriber) {
		s.OnInsert(sbx)
	})
}

func (m *Map) Remove(sandboxID string) {
	m.sandboxes.Remove(sandboxID)

	go m.trigger(func(s MapSubscriber) {
		s.OnRemove(sandboxID)
	})
}

func (m *Map) RemoveByExecutionID(sandboxID, executionID string) {
	removed := m.sandboxes.RemoveCb(sandboxID, func(_ string, v *Sandbox, exists bool) bool {
		if !exists {
			return false
		}

		if v == nil {
			return false
		}

		return v.Runtime.ExecutionID == executionID
	})

	if removed {
		go m.trigger(func(s MapSubscriber) {
			s.OnRemove(sandboxID)
		})
	}
}

func NewSandboxesMap() *Map {
	return &Map{sandboxes: smap.New[*Sandbox]()}
}
