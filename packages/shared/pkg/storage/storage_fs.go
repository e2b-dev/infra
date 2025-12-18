package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type fsStore struct {
	basePath string
	opened   map[string]*os.File
}

var _ StorageProvider = (*fsStore)(nil)

type fsObject struct {
	path string
}

var _ ObjectProvider = (*fsObject)(nil)

type fsFramedWriter struct {
	path string
	opts *CompressionOptions
}

var _ FramedWriter = (*fsFramedWriter)(nil)

type fsFramedReader struct {
	path string
	info *CompressedInfo
}

var _ FramedReader = (*fsFramedReader)(nil)

func newFSStore(basePath string) (*fsStore, error) {
	return &fsStore{
		basePath: basePath,
		opened:   make(map[string]*os.File),
	}, nil
}

func (fs *fsStore) DeleteObjectsWithPrefix(_ context.Context, prefix string) error {
	filePath := fs.abs(prefix)

	return os.RemoveAll(filePath)
}

func (fs *fsStore) GetDetails() string {
	return fmt.Sprintf("[Local file storage, base path set to %s]", fs.basePath)
}

func (fs *fsStore) UploadSignedURL(_ context.Context, _ string, _ time.Duration) (string, error) {
	return "", fmt.Errorf("file system storage does not support signed URLs")
}

func (fs *fsStore) OpenObject(_ context.Context, path string, _ ObjectType) (ObjectProvider, error) {
	dir := filepath.Dir(fs.abs(path))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	return &fsObject{
		path: fs.abs(path),
	}, nil
}

func (fs *fsStore) OpenFramedWriter(_ context.Context, path string, opts *CompressionOptions) (FramedWriter, error) {
	dir := filepath.Dir(fs.abs(path))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	return &fsFramedWriter{
		path: fs.abs(path),
		opts: opts,
	}, nil
}

func (fs *fsStore) OpenFramedReader(_ context.Context, path string, info *CompressedInfo) (FramedReader, error) {
	return &fsFramedReader{
		path: fs.abs(path),
		info: info,
	}, nil
}

func (fs *fsStore) abs(path string) string {
	return filepath.Join(fs.basePath, path)
}

func getHandle(path string, checkExistence bool) (*os.File, error) {
	if checkExistence {
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, ErrObjectNotExist
			}

			return nil, err
		}

		if info.IsDir() {
			return nil, fmt.Errorf("path %s is a directory", path)
		}
	}

	handle, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}

	return handle, nil
}

func (f *fsObject) WriteTo(_ context.Context, dst io.Writer) (int64, error) {
	handle, err := getHandle(f.path, true)
	if err != nil {
		return 0, err
	}

	defer handle.Close()

	return io.Copy(dst, handle)
}

func (f *fsObject) Write(_ context.Context, data []byte) (int, error) {
	handle, err := getHandle(f.path, false)
	if err != nil {
		return 0, err
	}
	defer handle.Close()

	count, err := handle.Write(data)

	return count, err
}

func (f *fsObject) CopyFromFileSystem(_ context.Context, path string) error {
	handle, err := getHandle(f.path, false)
	if err != nil {
		return err
	}
	defer handle.Close()

	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()

	_, err = io.Copy(handle, src)
	if err != nil {
		return err
	}

	return nil
}

func (f *fsObject) Exists(_ context.Context) (bool, error) {
	_, err := os.Stat(f.path)
	if os.IsNotExist(err) {
		return false, nil
	}

	return err == nil, err
}

func (f *fsObject) Delete(_ context.Context) error {
	return os.Remove(f.path)
}

func (f *fsFramedWriter) StoreFromFileSystem(_ context.Context, path string) (*CompressedInfo, error) {
	handle, err := getHandle(f.path, false)
	if err != nil {
		return nil, err
	}
	defer handle.Close()

	src, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	fe, err := newFrameEncoder(f.opts, func(frame []byte, _ bool) error {
		_, err := handle.Write(frame)

		return err
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create framed encoder: %w", err)
	}

	_, err = io.Copy(fe, src)
	if err != nil {
		return nil, err
	}

	return fe.Close()
}

func (f *fsFramedReader) ReadAt(_ context.Context, _ []byte, offset int64) (n int, err error) {
	panic("<>/<> REFACTOR ME")

	// handle, err := getHandle(f.path, true)
	// if err != nil {
	// 	return 0, err
	// }
	// defer handle.Close()

	// if f.info == nil || f.info.CompressionType == CompressionNone {
	// 	return handle.ReadAt(buf, offset)
	// }

	// if offset < f.info.FramesStartAt.U {
	// 	return 0, fmt.Errorf("offset %d is before start of framed data %d", offset, f.info.FramesStartAt.U)
	// }

	// dec, err := zstd.NewReader(nil)
	// if err != nil {
	// 	return 0, fmt.Errorf("failed to create zstd decoder: %w", err)
	// }
	// defer dec.Close()

	// first := true
	// err = f.info.Range(offset, int64(len(buf)), func(off Offset, frame Frame) error {
	// 	if first {
	// 		// seek to the start of the first frame
	// 		_, err = handle.Seek(off.C, io.SeekStart)
	// 		if err != nil {
	// 			return fmt.Errorf("failed to seek to frame at offset %d: %w", off.C, err)
	// 		}
	// 		first = false
	// 	}

	// 	// read the compressed frame
	// 	compressedFrame := make([]byte, frame.C)
	// 	_, err = io.ReadFull(handle, compressedFrame)
	// 	if err != nil {
	// 		return fmt.Errorf("failed to read compressed frame at offset %d: %w", off.C, err)
	// 	}

	// 	// decompress the frame
	// 	decompressedFrame, err := dec.DecodeAll(compressedFrame, nil)
	// 	if err != nil {
	// 		return fmt.Errorf("failed to decompress frame at offset %d: %w", off.C, err)
	// 	}

	// 	// calculate the offset within the decompressed frame to start copying from
	// 	startInFrame := 0
	// 	if offset > off.U {
	// 		startInFrame = int(offset - off.U)
	// 	}

	// 	// calculate how much data to copy from this frame
	// 	toCopy := min(len(buf), len(decompressedFrame)-startInFrame)

	// 	// copy the data to the buffer
	// 	copy(buf[:toCopy], decompressedFrame[startInFrame:startInFrame+toCopy])

	// 	// update the buffer and offset for the next iteration
	// 	buf = buf[toCopy:]
	// 	offset += int64(toCopy)

	// 	return nil
	// })
	// if err != nil {
	// 	return n, err
	// }

	// return len(buf), nil
}

func (f *fsFramedReader) Size(_ context.Context) (int64, error) {
	return f.info.TotalUncompressedSize(), nil
}
