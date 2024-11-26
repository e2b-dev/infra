package nbd

import (
	"context"
	"os"
	"sync"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
)

type ManagedPathMount struct {
	ctx context.Context

	overlay block.Device

	serverFile *os.File
	dev        *DirectPathMount

	wg sync.WaitGroup

	closeLock sync.Mutex

	errs chan error
}

func NewManagedPathMount(
	ctx context.Context,
	overlay block.Device,
) *ManagedPathMount {
	return &ManagedPathMount{
		ctx:     ctx,
		overlay: overlay,
		errs:    make(chan error),
	}
}

func (m *ManagedPathMount) Wait() error {
	for err := range m.errs {
		if err != nil {
			return err
		}
	}

	return nil
}

func (m *ManagedPathMount) Open(ctx context.Context) (uint32, int64, error) {
	size, err := m.overlay.Size()
	if err != nil {
		return 0, 0, err
	}

	m.dev = NewDirectPathMount(m.overlay)

	deviceIndex, err := m.dev.Open(ctx)
	if err != nil {
		return 0, 0, err
	}

	return deviceIndex, size, nil
}

func (m *ManagedPathMount) Close() error {
	m.closeLock.Lock()
	defer m.closeLock.Unlock()

	if m.dev != nil {
		m.dev.Close()
	}

	if m.serverFile != nil {
		_ = m.serverFile.Close()
	}

	m.wg.Wait()

	if m.errs != nil {
		close(m.errs)

		m.errs = nil
	}

	return nil
}
