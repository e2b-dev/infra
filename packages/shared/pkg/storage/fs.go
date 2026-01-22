package storage

import (
	"bytes"
	"context"
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

func NewFS(basePath string) *Provider {
	fs := &FileSystem{
		basePath: basePath,
		opened:   make(map[string]*os.File),
	}

	return &Provider{
		Basic:                    fs,
		Admin:                    fs,
		MultipartUploaderFactory: fs,
		RangeGetter:              fs,
	}
}

func (s *FileSystem) String() string {
	return fmt.Sprintf("[Local file storage, base path set to %s]", s.basePath)
}

func (s *FileSystem) DeleteWithPrefix(_ context.Context, prefix string) error {
	var err error
	defer func() {
		fmt.Printf("<>/<> FS DeleteWithPrefix done prefix=%s err=%v\n", prefix, err)
	}()

	filePath := s.getPath(prefix)

	err = os.RemoveAll(filePath)
	return err
}

func (s *FileSystem) getPath(path string) string {
	return filepath.Join(s.basePath, path)
}

func (s *FileSystem) StartDownload(_ context.Context, path string) (io.ReadCloser, error) {
	var err error
	defer func() {
		fmt.Printf("<>/<> FS StartDownload done path=%s err=%v\n", path, err)
	}()

	handle, err := s.mustExist(path)
	if err != nil {
		return nil, err
	}

	return handle, nil
}

func (s *FileSystem) Download(ctx context.Context, path string, dst io.Writer) (int64, error) {
	var err error
	var n int64
	defer func() {
		fmt.Printf("<>/<> FS Download done path=%s bytes=%d err=%v\n", path, n, err)
	}()

	handle, err := s.StartDownload(ctx, path)
	if err != nil {
		return 0, err
	}
	defer handle.Close()

	n, err = io.Copy(dst, handle)
	return n, err
}

func (s *FileSystem) Upload(_ context.Context, path string, in io.Reader) (int64, error) {
	var err error
	var n int64
	defer func() {
		fmt.Printf("<>/<> FS Upload done path=%s bytes=%d err=%v\n", path, n, err)
	}()

	handle, err := s.create(path)
	if err != nil {
		return 0, err
	}
	defer handle.Close()

	n, err = io.Copy(handle, in)
	return n, err
}

func (s *FileSystem) RangeGet(_ context.Context, path string, offset int64, length int) (io.ReadCloser, error) {
	var err error
	defer func() {
		fmt.Printf("<>/<> FS RangeGet done path=%s offset=%d length=%d err=%v\n", path, offset, length, err)
	}()

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

func (s *FileSystem) Size(_ context.Context, path string) (int64, error) {
	var err error
	var size int64
	defer func() {
		fmt.Printf("<>/<> FS Size done path=%s size=%d err=%v\n", path, size, err)
	}()

	handle, err := s.mustExist(path)
	if err != nil {
		return 0, err
	}
	defer handle.Close()

	fileInfo, err := handle.Stat()
	if err != nil {
		return 0, err
	}

	size = fileInfo.Size()
	return size, nil
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

func (s *FileSystem) MakeMultipartUpload(_ context.Context, objectPath string, _ RetryConfig) (MultipartUploader, func(), int, error) {
	var err error
	defer func() {
		fmt.Printf("<>/<> FS MakeMultipartUpload done path=%s err=%v\n", objectPath, err)
	}()

	return &fsMultipartUploader{
		fs:         s,
		objectPath: objectPath,
		parts:      map[int][]byte{},
	}, func() {}, 1, err
}

type fsMultipartUploader struct {
	fs         *FileSystem
	objectPath string
	parts      map[int][]byte
	started    bool
	mu         sync.Mutex
}

func (u *fsMultipartUploader) Start(_ context.Context) error {
	var err error
	defer func() {
		fmt.Printf("<>/<> FS Multipart Start done path=%s err=%v\n", u.objectPath, err)
	}()

	u.mu.Lock()
	defer u.mu.Unlock()
	if u.started {
		return err
	}
	u.started = true
	return err
}

func (u *fsMultipartUploader) UploadPart(_ context.Context, partNumber int, dataList ...[]byte) error {
	var err error
	if partNumber <= 0 {
		err = fmt.Errorf("invalid part number %d", partNumber)
		return err
	}

	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.started {
		err = fmt.Errorf("multipart upload not started")
		return err
	}

	total := 0
	for _, data := range dataList {
		total += len(data)
	}
	defer func() {
		fmt.Printf("<>/<> FS Multipart UploadPart done path=%s part=%d frames=%d total=%#x err=%v\n", u.objectPath, partNumber, len(dataList), total, err)
	}()

	buffer := make([]byte, 0, total)
	for _, data := range dataList {
		buffer = append(buffer, data...)
	}

	if u.parts == nil {
		u.parts = map[int][]byte{}
	}
	u.parts[partNumber] = buffer

	return err
}

func (u *fsMultipartUploader) Complete(_ context.Context) error {
	var err error
	defer func() {
		fmt.Printf("<>/<> FS Multipart Complete done path=%s err=%v\n", u.objectPath, err)
	}()

	u.mu.Lock()
	if !u.started {
		u.mu.Unlock()
		err = fmt.Errorf("multipart upload not started")
		return err
	}

	if len(u.parts) == 0 {
		u.mu.Unlock()
		err = fmt.Errorf("no parts uploaded")
		return err
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
			err = fmt.Errorf("missing part %d", i)
			return err
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
		err = err
		return err
	}
	defer handle.Close()

	reader := bytes.NewReader(bytes.Join(dataParts, nil))
	_, err = io.Copy(handle, reader)
	return err
}
