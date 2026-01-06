package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/lock"
)

var (
	ErrOffsetUnaligned = errors.New("offset must be a multiple of chunk size")
	ErrBufferTooSmall  = errors.New("buffer is too small")
	ErrMultipleChunks  = errors.New("cannot read multiple chunks")
	ErrBufferTooLarge  = errors.New("buffer is too large")
)

type cachedFramedReaderWriter struct {
	path      string
	chunkSize int64
	r         FramedReader
	w         FramedWriter
}

var (
	_ FramedReader = (*cachedFramedReaderWriter)(nil)
	_ FramedWriter = (*cachedFramedReaderWriter)(nil)
)

// When reading from the cache, we return only the exact chunk requested, see
// 'validateReadAtParams' for details. If we fall back to remote read, we read
// the entire set of frames that overlap with the requested range.
func (c cachedFramedReaderWriter) ReadFrames(ctx context.Context, off int64, n int, ft *FrameTable) (framesStartAt int64, frameData [][]byte, err error) {
	// fmt.Printf("<>/<> cachedFramedReaderWriter.ReadFrames: off %x, n %x\n", off, n) // DEBUG --- IGNORE ---

	ctx, span := tracer.Start(ctx, "CachedFileObjectProvider.ReadFrames", trace.WithAttributes(
		attribute.Int64("offset", off),
		attribute.Int("buff_len", n),
	))
	defer span.End()

	if err := c.validateReadAtParams(int64(n), off); err != nil {
		return 0, nil, err
	}

	// try to read from cache first
	chunkPath := c.makeChunkFilename(off)

	// readTimer := cacheReadTimerFactory.Begin()
	// buf := make([]byte, n)
	// count, err := c.readAtFromCache(ctx, chunkPath, buf)
	// if ignoreEOF(err) == nil {
	// 	cacheHits.Add(ctx, 1)
	// 	readTimer.End(ctx, int64(count))

	// 	return off, [][]byte{buf[:count]}, err // return `err` in case it's io.EOF
	// }
	cacheMisses.Add(ctx, 1)

	logger.L().Debug(ctx, "failed to read cached chunk, falling back to remote read",
		zap.String("chunk_path", chunkPath),
		zap.Int64("offset", off),
		zap.Error(err))

	// read remote file
	start, frames, err := c.r.ReadFrames(ctx, off, n, ft)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to perform uncached read: %w", err)
	}
	// fmt.Printf("<>/<> call remote ReadFrames off %#x, n %#x, got %d frames starting at %#x, frame[0] size: %#x\n", off, n, len(frames), start, len(frames[0])) // DEBUG --- IGNORE ---

	// start is always greater or equal to off
	skipToFirstChunk := ((off - start) / c.chunkSize) * c.chunkSize
	firstChunkOffset := start + skipToFirstChunk
	// fmt.Printf("<>/<> chunk size %#x, skip to first chunk %#x, first chunk offset %#x\n", c.chunkSize, skipToFirstChunk, firstChunkOffset) // DEBUG --- IGNORE ---
	for i, frame := range frames {
		// fmt.Printf("<>/<> inspecting frame %d of size %#x\n", i, len(frame)) // DEBUG --- IGNORE ---
		skip := min(int64(len(frame)), skipToFirstChunk)
		frames[i] = frame[skip:]
		// fmt.Printf("<>/<> skipToFirstChunk %#x, skip %#x, remaining frame size %#x\n", skipToFirstChunk, skip, len(frames[i])) // DEBUG --- IGNORE ---
		skipToFirstChunk -= skip
		if skipToFirstChunk <= 0 {
			break
		}
	}
	rr := newMultiReader(frames)

	go c.writeChunksToCache(context.WithoutCancel(ctx), firstChunkOffset, rr)

	return start, frames, nil
}

func (c cachedFramedReaderWriter) Size(ctx context.Context) (int64, error) {
	if size, ok := c.readLocalSize(ctx); ok {
		cacheHits.Add(ctx, 1)

		return size, nil
	}
	cacheMisses.Add(ctx, 1)

	size, err := c.r.Size(ctx)
	if err != nil {
		return 0, err
	}

	go c.writeLocalSize(ctx, size)

	return size, nil
}

func (c cachedFramedReaderWriter) StoreFromFileSystem(ctx context.Context, path string) (*FrameTable, error) {
	return c.w.StoreFromFileSystem(ctx, path)
}

func (c cachedFramedReaderWriter) makeChunkFilename(offset int64) string {
	return fmt.Sprintf("%s/%012d-%d.bin", c.path, offset/c.chunkSize, c.chunkSize)
}

func (c cachedFramedReaderWriter) makeTempChunkFilename(offset int64) string {
	tempFilename := uuid.NewString()

	return fmt.Sprintf("%s/.temp.%012d-%d.bin.%s", c.path, offset/c.chunkSize, c.chunkSize, tempFilename)
}

func (c cachedFramedReaderWriter) readAtFromCache(ctx context.Context, chunkPath string, buff []byte) (int, error) {
	var fp *os.File
	fp, err := os.Open(chunkPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open file: %w", err)
	}
	// fmt.Printf("<>/<> readAtFromCache: found the file at path %s\n", chunkPath) // DEBUG --- IGNORE ---

	defer cleanup(ctx, "failed to close chunk", fp.Close)

	count, err := fp.ReadAt(buff, 0) // offset is in the filename
	if ignoreEOF(err) != nil {
		return 0, fmt.Errorf("failed to read from chunk: %w", err)
	}
	// fmt.Printf("<>/<> read %#x bytes from file at offset 0\n", count) // DEBUG --- IGNORE ---

	return count, err // return `err` in case it's io.EOF
}

func (c cachedFramedReaderWriter) sizeFilename() string {
	return filepath.Join(c.path, "size.txt")
}

func (c cachedFramedReaderWriter) readLocalSize(ctx context.Context) (int64, bool) {
	fname := c.sizeFilename()
	content, err := os.ReadFile(fname)
	if err != nil {
		logger.L().Warn(ctx, "failed to read cached size, falling back to remote read",
			zap.String("path", fname),
			zap.Error(err))

		return 0, false
	}

	size, err := strconv.ParseInt(string(content), 10, 64)
	if err != nil {
		logger.L().Error(ctx, "failed to parse cached size, falling back to remote read",
			zap.String("path", fname),
			zap.String("content", string(content)),
			zap.Error(err))

		return 0, false
	}

	return size, true
}

func (c cachedFramedReaderWriter) validateReadAtParams(buffSize, offset int64) error {
	if buffSize == 0 {
		return ErrBufferTooSmall
	}
	if buffSize > c.chunkSize {
		return ErrBufferTooLarge
	}
	if offset%c.chunkSize != 0 {
		return ErrOffsetUnaligned
	}
	if (offset%c.chunkSize)+buffSize > c.chunkSize {
		return ErrMultipleChunks
	}

	return nil
}

func (c cachedFramedReaderWriter) writeChunksToCache(ctx context.Context, offset int64, r io.Reader) {
	var err error
	var path string
	for n := 0; err == nil; n++ {
		path = c.makeChunkFilename(offset)
		err = c.writeChunkToCache(ctx, path, offset+int64(n)*c.chunkSize, r)
	}
	if errors.Is(err, io.EOF) || err == nil {
		return
	}
	logger.L().Warn(ctx, "failed to save chunk to cache",
		zap.String("path", path), zap.Error(err))
}

func (c cachedFramedReaderWriter) writeChunkToCache(ctx context.Context, path string, offset int64, r io.Reader) (err error) {
	// Try to acquire lock for this chunk write to NFS cache
	lockFile, err := lock.TryAcquireLock(ctx, path)
	if err != nil {
		if errors.Is(err, lock.ErrLockAlreadyHeld) {
			// Another process is already writing this chunk, so we can skip writing it ourselves
			return nil
		}

		return err
	}

	// Release lock after write completes
	defer func() {
		releaseErr := lock.ReleaseLock(ctx, lockFile)
		if err == nil {
			err = releaseErr
		}

		return
	}()

	writeTimer := cacheWriteTimerFactory.Begin()

	tempPath := c.makeTempChunkFilename(offset)
	file, err := os.Create(tempPath)
	if err != nil {
		return fmt.Errorf("failed to create temp chunk file: %w", err)
	}

	_, err = io.CopyN(file, r, c.chunkSize)
	if err != nil {
		file.Close()
		return fmt.Errorf("failed to write to temp chunk file: %w", err)
	}
	file.Close()

	if err = moveWithoutReplace(ctx, tempPath, path); err != nil {
		return fmt.Errorf("failed to rename temp chunk file: %w", err)
	}

	writeTimer.End(ctx, int64(c.chunkSize))
	return nil
}

func (c cachedFramedReaderWriter) writeLocalSize(ctx context.Context, size int64) {
	finalFilename := c.sizeFilename()

	// Try to acquire lock for this chunk write to NFS cache
	lockFile, err := lock.TryAcquireLock(ctx, finalFilename)
	if err != nil {
		if errors.Is(err, lock.ErrLockAlreadyHeld) {
			// Another process is already writing this chunk, so we can skip writing it ourselves
			return
		}

		logger.L().Warn(ctx, "failed to acquire lock", zap.String("path", finalFilename), zap.Error(err))

		return
	}

	// Release lock after write completes
	defer func() {
		err := lock.ReleaseLock(ctx, lockFile)
		if err != nil {
			logger.L().Warn(ctx, "failed to release lock after writing chunk to cache",
				zap.Int64("size", size),
				zap.String("path", finalFilename),
				zap.Error(err))
		}
	}()

	tempFilename := filepath.Join(c.path, fmt.Sprintf(".size.bin.%s", uuid.NewString()))

	if err := os.WriteFile(tempFilename, fmt.Appendf(nil, "%d", size), cacheFilePermissions); err != nil {
		logger.L().Warn(ctx, "failed to write to temp file",
			zap.String("path", tempFilename),
			zap.Error(err))

		return
	}

	if err := moveWithoutReplace(ctx, tempFilename, finalFilename); err != nil {
		logger.L().Warn(ctx, "failed to move temp file",
			zap.String("temp_path", tempFilename),
			zap.String("final_path", finalFilename),
			zap.Error(err))

		return
	}
}
