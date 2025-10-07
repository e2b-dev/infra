package memory

import (
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type memorySandbox struct {
	_data sandbox.Sandbox

	transition *utils.ErrorOnce
	mu         sync.RWMutex
}

func newMemorySandbox(data sandbox.Sandbox) *memorySandbox {
	return &memorySandbox{
		_data: data,
	}
}

func (i *memorySandbox) SetExpired() {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.setExpired()
}

func (i *memorySandbox) setExpired() {
	if !i._data.IsExpired() {
		i._data.EndTime = time.Now()
	}
}

func (i *memorySandbox) Data() sandbox.Sandbox {
	i.mu.RLock()
	defer i.mu.RUnlock()

	return i._data
}

func (i *memorySandbox) State() sandbox.State {
	i.mu.RLock()
	defer i.mu.RUnlock()

	return i._data.State
}

// SandboxID returns the sandbox ID, safe to use without lock, it's immutable
func (i *memorySandbox) SandboxID() string {
	return i._data.SandboxID
}

// TeamID returns the team ID, safe to use without lock, it's immutable
func (i *memorySandbox) TeamID() uuid.UUID {
	return i._data.TeamID
}
