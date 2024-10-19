package cache

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/edsrzf/mmap-go"
	"github.com/jellydator/ttlcache/v3"
	"golang.org/x/sys/unix"

	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
)

const (
	mmapExpiration = time.Hour * 25
)

type Mmapfile struct {
	Map        *mmap.MMap
	EnsureOpen func() (*Mmapfile, error)
}

func (m *Mmapfile) ReadAt(p []byte, off int64) (n int, err error) {
	n = copy(p, (*m.Map)[off:])

	return n, nil
}

func (m *Mmapfile) Close() error {
	if m.Map == nil {
		return nil
	}

	err := m.Map.Unmap()
	if err != nil {
		return fmt.Errorf("failed to unmap region: %w", err)
	}

	return nil
}

func getMmapfile(logger *logs.SandboxLogger, path string) (*mmap.MMap, error) {
	f, err := os.OpenFile(path, os.O_RDONLY, 0o777)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	defer func() {
		closeErr := f.Close()
		if closeErr != nil {
			logger.Errorf("failed to close file: %v", closeErr)
		}
	}()

	mp, err := mmap.Map(f, unix.PROT_READ, mmap.RDONLY)
	if err != nil {
		return nil, fmt.Errorf("failed to map region: %w", err)
	}

	return &mp, nil
}

func newMmapfile(logger *logs.SandboxLogger, path string) *Mmapfile {
	h := &Mmapfile{}

	h.EnsureOpen = sync.OnceValues(func() (*Mmapfile, error) {
		mp, err := getMmapfile(logger, path)
		if err != nil {
			return nil, fmt.Errorf("failed to get mmapfile: %w", err)
		}

		h.Map = mp

		return h, err
	})

	return h
}

type MmapfileCache struct {
	cache *ttlcache.Cache[string, *Mmapfile]
}

func (c *MmapfileCache) GetMmapfile(logger *logs.SandboxLogger, path, id string) (*Mmapfile, error) {
	hugefile, _ := c.cache.GetOrSet(id, newMmapfile(logger, path), ttlcache.WithTTL[string, *Mmapfile](mmapExpiration))

	mp, err := hugefile.Value().EnsureOpen()
	if err != nil {
		c.cache.Delete(id)

		return nil, fmt.Errorf("failed to copy hugefile: %w", err)
	}

	return mp, nil
}

func NewMmapfileCache() *MmapfileCache {
	cache := ttlcache.New(
		ttlcache.WithTTL[string, *Mmapfile](mmapExpiration),
	)

	cache.OnEviction(func(ctx context.Context, reason ttlcache.EvictionReason, item *ttlcache.Item[string, *Mmapfile]) {
		err := item.Value().Close()
		if err != nil {
			fmt.Printf("failed to close mmapfile: %v", err)
		}
	})

	go cache.Start()

	return &MmapfileCache{
		cache: cache,
	}
}
