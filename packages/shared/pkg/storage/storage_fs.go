package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

type FileSystem struct {
	basePath string
	opened   map[string]*os.File
}

func NewFS(basePath string) *Backend {
	fs := &FileSystem{
		basePath: basePath,
		opened:   make(map[string]*os.File),
	}

	return &Backend{
		Basic:                    fs,
		Manager:                  fs,
		MultipartUploaderFactory: fs,
		RangeGetter:              fs,
	}
}

func (s *FileSystem) String() string {
	return fmt.Sprintf("[Local file storage, base path set to %s]", s.basePath)
}

func (s *FileSystem) DeleteWithPrefix(_ context.Context, prefix string) error {
	filePath := s.getPath(prefix)

	return os.RemoveAll(filePath)
}

func (s *FileSystem) getPath(path string) string {
	return filepath.Join(s.basePath, path)
}

func (s *FileSystem) StartDownload(_ context.Context, path string) (io.ReadCloser, error) {
	handle, err := s.mustExist(path)
	if err != nil {
		return nil, err
	}

	return handle, nil
}

func (s *FileSystem) Download(ctx context.Context, path string, dst io.Writer) (int64, error) {
	handle, err := s.StartDownload(ctx, path)
	if err != nil {
		return 0, err
	}
	defer handle.Close()

	return io.Copy(dst, handle)
}

func (s *FileSystem) Upload(_ context.Context, path string, in io.Reader) (int64, error) {
	handle, err := s.create(path)
	if err != nil {
		return 0, err
	}
	defer handle.Close()

	return io.Copy(handle, in)
}

func (s *FileSystem) RangeGet(_ context.Context, path string, offset int64, length int) (io.ReadCloser, error) {
	handle, err := s.mustExist(path)
	if err != nil {
		return nil, err
	}
	defer handle.Close()

	if length == 0 {
		return io.NopCloser(bytes.NewReader(nil)), nil
	}

	buf := make([]byte, length)
	n, err := handle.ReadAt(buf, offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}

	return io.NopCloser(bytes.NewReader(buf[:n])), nil
}

func (s *FileSystem) RawSize(_ context.Context, path string) (int64, error) {
	handle, err := s.mustExist(path)
	if err != nil {
		return 0, err
	}
	defer handle.Close()

	fileInfo, err := handle.Stat()
	if err != nil {
		return 0, err
	}

	return fileInfo.Size(), nil
}

func (s *FileSystem) mustExist(path string) (*os.File, error) {
	fullPath := s.getPath(path)
	info, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrObjectNotExist
		}

		return nil, err
	}

	if info.IsDir() {
		return nil, fmt.Errorf("path %s is a directory", fullPath)
	}

	return os.OpenFile(fullPath, os.O_RDWR|os.O_CREATE, 0o644)
}

func (s *FileSystem) create(path string) (*os.File, error) {
	fullPath := s.getPath(path)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return nil, err
	}

	return os.OpenFile(fullPath, os.O_RDWR|os.O_CREATE, 0o644)
}

func (s *FileSystem) MakeMultipartUpload(_ context.Context, objectPath string, _ RetryConfig, metadata map[string]string) (MultipartUploader, func(), int, error) {
	return &fsMultipartUploader{
		fs:         s,
		objectPath: objectPath,
		parts:      map[int][]byte{},
		metadata:   metadata,
	}, func() {}, 1, nil
}

type fsMultipartUploader struct {
	fs         *FileSystem
	objectPath string
	parts      map[int][]byte
	started    bool
	mu         sync.Mutex
	metadata   map[string]string
}

func (u *fsMultipartUploader) Start(_ context.Context) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.started {
		return nil
	}
	u.started = true

	return nil
}

func (u *fsMultipartUploader) UploadPart(_ context.Context, partNumber int, dataList ...[]byte) error {
	if partNumber <= 0 {
		return fmt.Errorf("invalid part number %d", partNumber)
	}

	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.started {
		return fmt.Errorf("multipart upload not started")
	}

	total := 0
	for _, data := range dataList {
		total += len(data)
	}

	buffer := make([]byte, 0, total)
	for _, data := range dataList {
		buffer = append(buffer, data...)
	}

	if u.parts == nil {
		u.parts = map[int][]byte{}
	}
	u.parts[partNumber] = buffer

	return nil
}

func (u *fsMultipartUploader) Complete(_ context.Context) error {
	u.mu.Lock()
	if !u.started {
		u.mu.Unlock()

		return fmt.Errorf("multipart upload not started")
	}

	if len(u.parts) == 0 {
		u.mu.Unlock()

		return fmt.Errorf("no parts uploaded")
	}

	partNumbers := make([]int, 0, len(u.parts))
	for partNumber := range u.parts {
		partNumbers = append(partNumbers, partNumber)
	}
	sort.Ints(partNumbers)

	maxPart := partNumbers[len(partNumbers)-1]
	for i := 1; i <= maxPart; i++ {
		if _, ok := u.parts[i]; !ok {
			u.mu.Unlock()

			return fmt.Errorf("missing part %d", i)
		}
	}

	dataParts := make([][]byte, 0, len(partNumbers))
	for _, partNumber := range partNumbers {
		dataParts = append(dataParts, u.parts[partNumber])
	}
	u.parts = nil
	u.mu.Unlock()

	handle, err := u.fs.create(u.objectPath)
	if err != nil {
		return err
	}
	defer handle.Close()

	reader := bytes.NewReader(bytes.Join(dataParts, nil))
	_, err = io.Copy(handle, reader)
	if err != nil {
		return err
	}

	// Write metadata sidecar file if metadata is provided.
	if len(u.metadata) > 0 {
		metaPath := u.fs.metaPath(u.objectPath)
		if err := os.MkdirAll(filepath.Dir(metaPath), 0o755); err != nil {
			return fmt.Errorf("failed to create metadata directory: %w", err)
		}

		data, err := json.Marshal(u.metadata)
		if err != nil {
			return fmt.Errorf("failed to marshal metadata: %w", err)
		}

		if err := os.WriteFile(metaPath, data, 0o644); err != nil {
			return fmt.Errorf("failed to write metadata file: %w", err)
		}
	}

	return nil
}

func (s *FileSystem) metaPath(path string) string {
	return s.getPath(path) + ".meta"
}

func (s *FileSystem) Size(ctx context.Context, path string) (virtSize, rawSize int64, err error) {
	rawSize, err = s.RawSize(ctx, path)
	if err != nil {
		return 0, 0, err
	}

	metaPath := s.metaPath(path)

	data, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			// No metadata file means no compression, virt == raw.
			return rawSize, rawSize, nil
		}

		return 0, 0, fmt.Errorf("failed to read metadata file: %w", err)
	}

	var metadata map[string]string
	if err := json.Unmarshal(data, &metadata); err != nil {
		return 0, 0, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}

	if uncompressedStr, ok := metadata[MetadataKeyUncompressedSize]; ok {
		if _, err := fmt.Sscanf(uncompressedStr, "%d", &virtSize); err == nil {
			return virtSize, rawSize, nil
		}
	}

	// No uncompressed size in metadata - virt == raw.
	return rawSize, rawSize, nil
}
