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
	hugefileExpiration = time.Hour * 25
	hugepagesDir       = "/mnt/hugepages"
	EnvsDirName        = "envs"
	copyBufferSize     = 128 * 1024 * 1024
)

type Hugefile struct {
	mp         *mmap.MMap
	EnsureCopy func() (string, error)
}

func (h *Hugefile) copy(originFilePath string, envID string, buildID string) (string, error) {
	hugefileDir := filepath.Join(hugepagesDir, EnvsDirName, envID, BuildDirName, buildID)

	err := os.MkdirAll(hugefileDir, 0o777)
	if err != nil {
		return "", fmt.Errorf("failed to create hugefile directory: %w", err)
	}

	hugefilePath := filepath.Join(hugefileDir, MemfileName)

	s, err := os.Stat(originFilePath)
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

	originalFile, err := os.Open(originFilePath)
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

	err = mp.Flush()
	if err != nil {
		return "", fmt.Errorf("failed to flush hugefile: %w", err)
	}

	return hugefilePath, nil
}

func (h *Hugefile) Close() error {
	if h.mp == nil {
		return nil
	}

	return h.mp.Unmap()
}

func NewHugefile(originFilePath string, envID string, buildID string) *Hugefile {
	h := &Hugefile{}

	h.EnsureCopy = sync.OnceValues[string, error](func() (string, error) {
		hugefilePath, err := h.copy(originFilePath, envID, buildID)
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
func (c *HugefileCache) GetHugefilePath(originFilePath string, envID string, buildID string) (string, error) {
	key := fmt.Sprintf("%s_%s", envID, buildID)

	hugefile, _ := c.cache.GetOrSet(key, NewHugefile(originFilePath, envID, buildID), ttlcache.WithTTL[string, *Hugefile](hugefileExpiration))

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
