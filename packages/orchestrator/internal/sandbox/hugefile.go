package sandbox

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/edsrzf/mmap-go"
	"github.com/jellydator/ttlcache/v3"
	"golang.org/x/sys/unix"
)

const (
	hugefileExpiration = time.Hour * 24
	hugepagesDir       = "/mnt/hugepages"
	copyBufferSize     = 128 * 1024 * 1024
)

type Hugefile struct {
	mp         *mmap.MMap
	mu         sync.Mutex
	EnsureCopy func() (string, error)
}

func (h *Hugefile) copy(path string) (string, error) {
	hugefilePath := filepath.Join(hugepagesDir, path)

	s, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("failed to stat hugefile: %w", err)
	}

	hugefile, err := os.OpenFile(hugefilePath, os.O_CREATE|os.O_RDWR, 0o777)
	if err != nil {
		return "", fmt.Errorf("failed to open hugefile: %w", err)
	}

	defer hugefile.Close()

	mp, err := mmap.MapRegion(hugefile, int(s.Size()), unix.PROT_READ|unix.PROT_WRITE, mmap.RDWR, 0)
	if err != nil {
		return "", fmt.Errorf("failed to mmap hugefile: %w", err)
	}

	originalFile, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open original file: %w", err)
	}

	defer originalFile.Close()

	buf := make([]byte, copyBufferSize)
	offset := 0
	for {
		n, err := originalFile.Read(buf)
		if err != nil && err != io.EOF {
			return "", fmt.Errorf("failed to read from original file: %w", err)
		}

		if n == 0 {
			break
		}

		copy(mp[offset:offset+n], buf[:n])

		offset += n
	}

	h.mp = &mp

	return hugefilePath, nil
}

func (h *Hugefile) Close() error {
	if h.mp == nil {
		return nil
	}

	return h.mp.Unmap()
}

func NewHugefile(path string) *Hugefile {
	h := &Hugefile{}

	h.EnsureCopy = sync.OnceValues[string, error](func() (string, error) {
		hugefilePath, err := h.copy(path)
		if err != nil {
			return "", fmt.Errorf("failed to copy hugefile: %w", err)
		}

		return hugefilePath, nil
	})

	return h
}

// Sudden exit of the orchestrator will leave hugefiles in the filesystem.
type HugefileCache struct {
	cache *ttlcache.Cache[string, *Hugefile]
}

// TODO: Do we need to flush the changes?
// TODO: Does FC unlink the mmaped file?
func (c *HugefileCache) GetHugefilePath(originFilePath string) (string, error) {
	hugefile, _ := c.cache.GetOrSet(originFilePath, NewHugefile(originFilePath), ttlcache.WithTTL[string, *Hugefile](hugefileExpiration))

	path, err := hugefile.Value().EnsureCopy()
	if err != nil {
		return "", fmt.Errorf("failed to copy hugefile: %w", err)
	}

	return path, nil
}

func NewHugefileCache() *HugefileCache {
	cache := ttlcache.New[string, *Hugefile](
		ttlcache.WithTTL[string, *Hugefile](hugefileExpiration),
	)

	go cache.Start()

	return &HugefileCache{
		cache: cache,
	}
}
