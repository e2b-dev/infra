package nbd

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/pojntfx/go-nbd/pkg/backend"
	bbackend "github.com/pojntfx/r3map/pkg/backend"
	"github.com/pojntfx/r3map/pkg/chunks"
)

const (
	pushWorkers  = 512
	pullWorkers  = 512
	pushInterval = time.Second * 20
)

type ManagedPathMount struct {
	ctx context.Context

	remote,
	local,
	syncer backend.Backend

	chunkSize int64

	serverFile *os.File
	pusher     *chunks.Pusher
	puller     *chunks.Puller
	dev        *DirectPathMount

	wg sync.WaitGroup

	closeLock sync.Mutex

	errs chan error
}

func NewManagedPathMount(
	ctx context.Context,

	remote backend.Backend,
	local backend.Backend,
	chunkSize int64,
) *ManagedPathMount {
	return &ManagedPathMount{
		ctx: ctx,

		remote: remote,
		local:  local,

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

func (m *ManagedPathMount) Open(ctx context.Context) (string, int64, error) {
	size, err := m.remote.Size()
	if err != nil {
		return "", 0, err
	}
	chunkCount := size / m.chunkSize

	deviceIndex, err := Pool.GetDeviceIndex(ctx)
	if err != nil {
		return "", 0, err
	}

	m.pusher = chunks.NewPusher(
		m.ctx,
		m.local,
		m.remote,
		m.chunkSize,
		pushInterval,
	)

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()

		if err := m.pusher.Wait(); err != nil {
			m.errs <- err

			return
		}
	}()

	if err := m.pusher.Open(pushWorkers); err != nil {
		return "", 0, err
	}

	local := m.pusher

	syncedReadWriter := chunks.NewSyncedReadWriterAt(m.remote, local, func(off int64) error {
		if err := local.MarkOffsetPushable(off); err != nil {
			return err
		}

		return nil
	})

	m.puller = chunks.NewPuller(
		m.ctx,
		syncedReadWriter,
		m.chunkSize,
		chunkCount,
		func(off int64) int64 {
			return 1
		},
	)

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()

		if err := m.puller.Wait(); err != nil {
			m.errs <- err

			return
		}
	}()

	if err := m.puller.Open(pullWorkers); err != nil {
		return "", 0, err
	}

	m.puller.Finalize([]int64{})

	arbitraryReadWriter := chunks.NewArbitraryReadWriterAt(syncedReadWriter, m.chunkSize)

	m.syncer = bbackend.NewReaderAtBackend(
		arbitraryReadWriter,
		func() (int64, error) {
			return size, nil
		},
		func() error {
			// We only ever touch the remote if we want to push
			_, err := local.Sync()
			if err != nil {
				return err
			}

			return nil
		},
		false,
	)

	m.dev = NewDirectPathMount(
		m.syncer,
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

	if m.syncer != nil {
		_ = m.syncer.Sync()
	}

	if m.dev != nil {
		m.dev.Close()
	}

	if m.puller != nil {
		_ = m.puller.Close()
	}

	if m.pusher != nil {
		_ = m.pusher.Close()
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

	return m.syncer.Sync()
}
