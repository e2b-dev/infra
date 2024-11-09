package nbd

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/pojntfx/go-nbd/pkg/backend"
	"github.com/pojntfx/go-nbd/pkg/client"
	bbackend "github.com/pojntfx/r3map/pkg/backend"
	"github.com/pojntfx/r3map/pkg/chunks"
)

type ManagedMountOptions struct {
	ChunkSize int64

	PullWorkers  int64
	PullPriority func(off int64) int64
	PullFirst    bool

	PushWorkers  int64
	PushInterval time.Duration

	Verbose bool
}

type ManagedMountHooks struct {
	OnBeforeSync func() error

	OnBeforeClose func() error

	OnChunkIsLocal func(off int64) error
}

type ManagedPathMount struct {
	ctx context.Context

	remote,
	local,
	syncer backend.Backend

	options *ManagedMountOptions
	hooks   *ManagedMountHooks

	serverOptions *Options
	clientOptions *client.Options

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

	options *ManagedMountOptions,
	hooks *ManagedMountHooks,

	serverOptions *Options,
	clientOptions *client.Options,
) *ManagedPathMount {
	if options == nil {
		options = &ManagedMountOptions{}
	}

	if options.ChunkSize <= 0 {
		options.ChunkSize = client.MaximumBlockSize
	}

	if options.PullWorkers < 0 {
		options.PullWorkers = 512
	}

	if options.PullPriority == nil {
		options.PullPriority = func(off int64) int64 {
			return 1
		}
	}

	if options.PushWorkers < 0 {
		options.PushWorkers = 512
	}

	if options.PushInterval == 0 {
		options.PushInterval = time.Second * 20
	}

	if hooks == nil {
		hooks = &ManagedMountHooks{}
	}

	return &ManagedPathMount{
		ctx: ctx,

		remote: remote,
		local:  local,

		options: options,
		hooks:   hooks,

		serverOptions: serverOptions,
		clientOptions: clientOptions,

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
	chunkCount := size / m.options.ChunkSize

	devicePath, err := Pool.GetDevice(ctx)
	if err != nil {
		return "", 0, err
	}

	var local chunks.ReadWriterAt
	if m.options.PushWorkers > 0 {
		m.pusher = chunks.NewPusher(
			m.ctx,
			m.local,
			m.remote,
			m.options.ChunkSize,
			m.options.PushInterval,
		)

		m.wg.Add(1)
		go func() {
			defer m.wg.Done()

			if err := m.pusher.Wait(); err != nil {
				m.errs <- err

				return
			}
		}()

		if err := m.pusher.Open(m.options.PushWorkers); err != nil {
			return "", 0, err
		}

		local = m.pusher
	} else {
		local = m.local
	}

	syncedReadWriter := chunks.NewSyncedReadWriterAt(m.remote, local, func(off int64) error {
		if m.options.PushWorkers > 0 {
			if err := local.(*chunks.Pusher).MarkOffsetPushable(off); err != nil {
				return err
			}

			if hook := m.hooks.OnChunkIsLocal; hook != nil {
				if err := hook(off); err != nil {
					return err
				}
			}
		}

		return nil
	})

	if m.options.PullWorkers > 0 {
		m.puller = chunks.NewPuller(
			m.ctx,
			syncedReadWriter,
			m.options.ChunkSize,
			chunkCount,
			func(off int64) int64 {
				return m.options.PullPriority(off)
			},
		)

		if !m.options.PullFirst {
			m.wg.Add(1)
			go func() {
				defer m.wg.Done()

				if err := m.puller.Wait(); err != nil {
					m.errs <- err

					return
				}
			}()
		}

		if err := m.puller.Open(m.options.PullWorkers); err != nil {
			return "", 0, err
		}

		m.puller.Finalize([]int64{})

		if m.options.PullFirst {
			if err := m.puller.Wait(); err != nil {
				return "", 0, err
			}
		}
	}

	arbitraryReadWriter := chunks.NewArbitraryReadWriterAt(syncedReadWriter, m.options.ChunkSize)

	m.syncer = bbackend.NewReaderAtBackend(
		arbitraryReadWriter,
		func() (int64, error) {
			return size, nil
		},
		func() error {
			if hook := m.hooks.OnBeforeSync; hook != nil {
				if err := hook(); err != nil {
					return err
				}
			}

			// We only ever touch the remote if we want to push
			if m.options.PushWorkers > 0 {
				_, err := local.(*chunks.Pusher).Sync()
				if err != nil {
					return err
				}
			}

			return nil
		},
		m.options.Verbose,
	)

	m.dev = NewDirectPathMount(
		m.syncer,
		devicePath,

		m.serverOptions,
		m.clientOptions,
	)

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()

		if err := m.dev.Wait(); err != nil {
			m.errs <- err

			return
		}
	}()

	if err := m.dev.Open(); err != nil {
		return "", 0, err
	}

	return devicePath, size, nil
}

func (m *ManagedPathMount) Close() error {
	m.closeLock.Lock()
	defer m.closeLock.Unlock()

	if m.syncer != nil {
		_ = m.syncer.Sync()
	}

	if hook := m.hooks.OnBeforeClose; hook != nil {
		if err := hook(); err != nil {
			return err
		}

		m.hooks.OnBeforeClose = nil // Don't call close hook multiple times
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
