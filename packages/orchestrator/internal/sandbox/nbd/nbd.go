package nbd

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/block"
)

type ManagedPathMount struct {
	ctx context.Context

	overlay block.Device

	chunkSize int64

	serverFile *os.File
	dev        *DirectPathMount

	wg sync.WaitGroup

	closeLock sync.Mutex

	errs chan error
}

func NewManagedPathMount(
	ctx context.Context,
	overlay block.Device,
	chunkSize int64,
) *ManagedPathMount {
	return &ManagedPathMount{
		ctx: ctx,

		overlay: overlay,

		chunkSize: chunkSize,

		errs: make(chan error),
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

func (m *ManagedPathMount) Open(ctx context.Context, deviceIndex uint32) (string, int64, error) {
	size, err := m.overlay.Size()
	if err != nil {
		return "", 0, err
	}

	m.dev = NewDirectPathMount(
		m.overlay,
		uint32(deviceIndex),
	)

	if err := m.dev.Open(); err != nil {
		return "", 0, err
	}

	return fmt.Sprintf("/dev/nbd%d", deviceIndex), size, nil
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

func (m *ManagedPathMount) Sync() error {
	// No need to call `OnBeforeSync` here, the syncer already calls this internally

	m.overlay.Sync()

	return nil
}
