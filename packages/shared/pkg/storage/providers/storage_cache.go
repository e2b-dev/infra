package providers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/shared/pkg/storage/providers")

const (
	cacheFilePermissions      = 0o600
	cacheDirPermissions       = 0o700
	maxCacheWriterConcurrency = 10
)

var (
	cacheRootPath  = env.GetEnv("SHARED_CHUNK_CACHE_PATH", "")
	cacheOpWriteTo = attribute.String("cache_op", "write_to")
	cacheOpReadAt  = attribute.String("cache_op", "read_at")
	cacheOpSize    = attribute.String("cache_op", "size")
)

func IsCacheEnabled() bool {
	return cacheRootPath != ""
}

var (
	meter                 = otel.GetMeterProvider().Meter("shared.pkg.storage")
	cacheReadTimerFactory = utils.Must(telemetry.NewTimerFactory(meter,
		"orchestrator.storage.cache.read",
		"Duration of cached reads",
		"Total cached bytes read",
		"Total cached reads",
	))
	cacheWriteTimerFactory = utils.Must(telemetry.NewTimerFactory(meter,
		"orchestrator.storage.cache.write",
		"Duration of cache writes",
		"Total bytes written to the cache",
		"Total writes to the cache",
	))
	cacheHits = utils.Must(meter.Int64Counter("orchestrator.storage.cache.hits",
		metric.WithDescription("total cache hits")))
	cacheMisses = utils.Must(meter.Int64Counter("orchestrator.storage.cache.misses",
		metric.WithDescription("total cache misses")))
)

type CachedProvider struct {
	chunkSize int64
	inner     storage.StorageProvider
	rootPath  string
}

var _ storage.StorageProvider = (*CachedProvider)(nil)

func NewCachedProvider(inner storage.StorageProvider) *CachedProvider {
	return &CachedProvider{rootPath: cacheRootPath, inner: inner, chunkSize: storage.MemoryChunkSize}
}

func (c CachedProvider) DeleteObjectsWithPrefix(ctx context.Context, prefix string) error {
	go func(ctx context.Context) {
		c.deleteObjectsWithPrefix(prefix)
	}(context.WithoutCancel(ctx))

	return c.inner.DeleteObjectsWithPrefix(ctx, prefix)
}

func (c CachedProvider) UploadSignedURL(ctx context.Context, path string, ttl time.Duration) (string, error) {
	return c.inner.UploadSignedURL(ctx, path, ttl)
}

func (c CachedProvider) OpenObject(ctx context.Context, path string) (storage.StorageObjectProvider, error) {
	innerObject, err := c.inner.OpenObject(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("failed to open object: %w", err)
	}

	localPath := filepath.Join(c.rootPath, path)
	if err = os.MkdirAll(localPath, cacheDirPermissions); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	return &CachedFileObjectProvider{path: localPath, chunkSize: c.chunkSize, inner: innerObject}, nil
}

func (c CachedProvider) GetDetails() string {
	return fmt.Sprintf("[Caching file storage, base path set to %s, which wraps %s]",
		c.rootPath, c.inner.GetDetails())
}

func (c CachedProvider) deleteObjectsWithPrefix(prefix string) {
	fullPrefix := filepath.Join(c.rootPath, prefix)
	if err := os.RemoveAll(fullPrefix); err != nil {
		zap.L().Error("failed to remove object with prefix",
			zap.String("prefix", prefix),
			zap.String("path", fullPrefix),
			zap.Error(err))
	}
}

type CachedFileObjectProvider struct {
	path      string
	chunkSize int64
	inner     storage.StorageObjectProvider
}

var _ storage.StorageObjectProvider = (*CachedFileObjectProvider)(nil)

// WriteTo is used for very small files, and we can check against their size to ensure the content is valid.
func (c *CachedFileObjectProvider) WriteTo(ctx context.Context, dst io.Writer) (written int64, err error) {
	ctx, span := tracer.Start(ctx, "CachedFileObjectProvider.WriteTo")
	defer span.End()

	if bytesRead, ok := c.copyFullFileFromCache(ctx, dst); ok {
		cacheHits.Add(ctx, 1, metric.WithAttributes(cacheOpWriteTo))
		return bytesRead, nil
	}
	cacheMisses.Add(ctx, 1, metric.WithAttributes(cacheOpWriteTo))

	return c.readAndCacheFullRemoteFile(ctx, dst)
}

func (c *CachedFileObjectProvider) WriteFromFileSystem(ctx context.Context, path string) (err error) {
	ctx, span := tracer.Start(ctx, "CachedFileObjectProvider.WriteFromFileSystem",
		trace.WithAttributes(attribute.String("path", path)))
	defer func() {
		recordError(span, err)
		span.End()
	}()

	// write the file to the disk and the remote system at the same time.
	// this opens the file twice, but the API makes it difficult to use a MultiWriter

	go func() {
		if err := c.createCacheBlocksFromFile(context.WithoutCancel(ctx), path); err != nil {
			zap.L().Error("failed to create cache blocks from file",
				zap.String("path", path),
				zap.Error(err),
			)
		}
	}()

	if err := c.inner.WriteFromFileSystem(ctx, path); err != nil {
		return fmt.Errorf("failed to write to remote storage: %w", err)
	}

	return nil
}

func (c *CachedFileObjectProvider) Write(ctx context.Context, src []byte) (num int, err error) {
	ctx, span := tracer.Start(ctx, "CachedFileObjectProvider.Write", trace.WithAttributes(attribute.Int("size", len(src))))
	defer func() {
		recordError(span, err)
		span.End()
	}()

	num, err = c.writeCacheAndRemote(ctx, src)
	if err != nil {
		return 0, err
	} else if num != len(src) {
		return 0, fmt.Errorf("expected %d bytes, only got %d bytes", len(src), num)
	}

	return num, nil
}

func (c *CachedFileObjectProvider) ReadAt(ctx context.Context, buff []byte, offset int64) (readCount int, err error) {
	ctx, span := tracer.Start(ctx, "CachedFileObjectProvider.ReadAt", trace.WithAttributes(
		attribute.Int64("offset", offset),
		attribute.Int("buff_len", len(buff)),
	))
	defer func() {
		recordError(span, err)
		span.End()
	}()

	if err := c.validateReadAtParams(int64(len(buff)), offset); err != nil {
		return 0, err
	}

	// try to read from cache first
	chunkPath := c.makeChunkFilename(offset)

	readTimer := cacheReadTimerFactory.Begin()
	count, err := c.readAtFromCache(chunkPath, buff)
	if ignoreEOF(err) == nil {
		cacheHits.Add(ctx, 1, metric.WithAttributes(cacheOpReadAt))
		readTimer.End(ctx, int64(count))
		span.SetAttributes(attribute.String("read-from", "local"))
		return count, err // return `err` in case it's io.EOF
	}
	cacheMisses.Add(ctx, 1, metric.WithAttributes(cacheOpReadAt))

	zap.L().Debug("failed to read cached chunk, falling back to remote read",
		zap.String("chunk_path", chunkPath),
		zap.Int64("offset", offset),
		zap.Error(err))

	// read remote file
	readCount, err = c.inner.ReadAt(ctx, buff, offset)
	if err != nil {
		return 0, fmt.Errorf("failed to perform uncached read: %w", err)
	}

	go func(count int) {
		c.writeChunkToCache(context.WithoutCancel(ctx), offset, chunkPath, buff[:count])
	}(readCount)

	span.SetAttributes(attribute.String("read-from", "remote"))
	return readCount, nil
}

func (c *CachedFileObjectProvider) Size(ctx context.Context) (int64, error) {
	if size, ok := c.readLocalSize(); ok {
		cacheHits.Add(ctx, 1, metric.WithAttributes(cacheOpSize))
		return size, nil
	}
	cacheMisses.Add(ctx, 1, metric.WithAttributes(cacheOpSize))

	size, err := c.inner.Size(ctx)
	if err != nil {
		return 0, err
	}

	go c.writeLocalSize(size)

	return size, nil
}

func (c *CachedFileObjectProvider) Delete(ctx context.Context) error {
	go func() {
		if err := os.RemoveAll(c.path); ignoreFileMissingError(err) != nil {
			zap.L().Error("error on cache delete", zap.String("path", c.path), zap.Error(err))
		}
	}()

	return c.inner.Delete(ctx)
}

// writeCacheAndRemote simultaneously writes a full file to both local cache and the remote persistence store. It does
// not need to worry about race conditions, as the files will only exist on the local machine, and can't be generated
// in parallel on any other machines.
func (c *CachedFileObjectProvider) writeCacheAndRemote(ctx context.Context, src []byte) (size int, err error) {
	ctx, span := tracer.Start(ctx, "CachedFileObjectProvider.writeCacheAndRemote")
	defer func() {
		recordError(span, err)
		span.End()
	}()

	size, err = c.inner.Write(ctx, src)
	if err != nil {
		return 0, fmt.Errorf("failed to remote write from byte array: %w", err)
	}
	if size != len(src) {
		zap.L().Warn("remote write didn't match data length",
			zap.Int("expected_size", len(src)),
			zap.Int("actual_size", size),
			zap.String("root_path", c.path),
		)
	}

	chunkSize := int(c.chunkSize)
	for offset := 0; offset < size; offset += chunkSize {
		// read from the source
		offsetEnd := min(offset+chunkSize, size)
		buf := src[offset:offsetEnd]

		go func(offset int, buf []byte) {
			// write to the cache file
			filename := c.makeChunkFilename(int64(offset))
			if err2 := os.WriteFile(filename, buf, cacheFilePermissions); err2 != nil {
				safelyRemoveFile(filename)
				zap.L().Warn("failed to write chunk file",
					zap.String("filename", filename),
					zap.Error(err2))
			}
		}(offset, buf)
	}

	return size, nil
}

var (
	ErrOffsetUnaligned = errors.New("offset must be a multiple of chunk size")
	ErrBufferTooSmall  = errors.New("buffer is too small")
	ErrMultipleChunks  = errors.New("cannot read multiple chunks")
	ErrBufferTooLarge  = errors.New("buffer is too large")
)

func (c *CachedFileObjectProvider) readLocalSize() (int64, bool) {
	fname := c.sizeFilename()
	content, err := os.ReadFile(fname)
	if err != nil {
		zap.L().Warn("failed to read cached size, falling back to remote read",
			zap.String("path", fname),
			zap.Error(err))
		return 0, false
	}

	size, err := strconv.ParseInt(string(content), 10, 64)
	if err != nil {
		zap.L().Error("failed to parse cached size, falling back to remote read",
			zap.String("path", fname),
			zap.String("content", string(content)),
			zap.Error(err))
		return 0, false
	}

	return size, true
}

func (c *CachedFileObjectProvider) validateReadAtParams(buffSize, offset int64) error {
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

func (c *CachedFileObjectProvider) sizeFilename() string {
	return filepath.Join(c.path, "size.txt")
}

func (c *CachedFileObjectProvider) createCacheBlocksFromFile(ctx context.Context, inputPath string) (err error) {
	ctx, span := tracer.Start(ctx, "CachedFileObjectProvider.createCacheBlocksFromFile")
	defer func() {
		recordError(span, err)
		span.End()
	}()

	input, err := os.Open(inputPath)
	if err != nil {
		return fmt.Errorf("failed to open input file: %w", err)
	}
	defer cleanup("failed to close file", input.Close)

	stat, err := input.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat input file: %w", err)
	}

	totalSize := stat.Size()
	var wg sync.WaitGroup
	workers := make(chan struct{}, maxCacheWriterConcurrency)
	for offset := int64(0); offset < totalSize; offset += c.chunkSize {
		wg.Add(1)
		go func(offset int64) {
			defer wg.Done()

			// limit concurrency
			workers <- struct{}{}
			defer func() { <-workers }()

			if err := c.writeChunkFromFile(ctx, offset, input); err != nil {
				zap.L().Error("failed to write chunk file",
					zap.String("path", inputPath),
					zap.Int64("offset", offset),
					zap.Error(err))
			}
		}(offset)
	}
	wg.Wait()

	return nil
}

type offsetReader struct {
	wrapped io.ReaderAt
	offset  int64
}

var _ io.Reader = (*offsetReader)(nil)

func (r *offsetReader) Read(p []byte) (n int, err error) {
	n, err = r.wrapped.ReadAt(p, r.offset)
	r.offset += int64(n)
	return
}

func newOffsetReader(file *os.File, offset int64) *offsetReader {
	return &offsetReader{file, offset}
}

// writeChunkFromFile writes a piece of a local file. It does not need to worry about race conditions, as it will only
// be called when building templates, and templates cannot be built on multiple machines at the same time.x
func (c *CachedFileObjectProvider) writeChunkFromFile(ctx context.Context, offset int64, input *os.File) (err error) {
	_, span := tracer.Start(ctx, "write chunk-from-file", trace.WithAttributes(
		attribute.Int64("offset", offset),
	))
	defer func() {
		recordError(span, err)
		span.End()
	}()

	chunkPath := c.makeChunkFilename(offset)
	span.SetAttributes(attribute.String("chunk_path", chunkPath))

	output, err := os.OpenFile(chunkPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, cacheFilePermissions)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %w", chunkPath, err)
	}
	defer cleanup("failed to close file", output.Close)

	offsetReader := newOffsetReader(input, offset)
	if _, err := io.CopyN(output, offsetReader, c.chunkSize); ignoreEOF(err) != nil {
		safelyRemoveFile(chunkPath)
		return fmt.Errorf("failed to copy chunk: %w", err)
	}

	return err // in case err == io.EOF
}

func ignoreFileMissingError(err error) error {
	if os.IsNotExist(err) {
		return nil
	}

	return err
}

func (c *CachedFileObjectProvider) writeLocalSize(size int64) {
	tempFilename := filepath.Join(c.path, fmt.Sprintf(".size.bin.%s", uuid.NewString()))

	if err := os.WriteFile(tempFilename, []byte(fmt.Sprintf("%d", size)), cacheFilePermissions); err != nil {
		zap.L().Warn("failed to write to temp file",
			zap.String("path", tempFilename),
			zap.Error(err))
		return
	}

	finalFilename := c.sizeFilename()
	if err := moveWithoutReplace(tempFilename, finalFilename); err != nil {
		zap.L().Warn("failed to move temp file",
			zap.String("temp_path", tempFilename),
			zap.String("final_path", finalFilename),
			zap.Error(err))
		return
	}
}

func (c *CachedFileObjectProvider) tempFullFilename() string {
	tempFilename := uuid.NewString()

	return fmt.Sprintf("%s/.temp.content.bin.%s", c.path, tempFilename)
}

func (c *CachedFileObjectProvider) fullFilename() string {
	return fmt.Sprintf("%s/content.bin", c.path)
}

func (c *CachedFileObjectProvider) makeTempChunkFilename(offset int64) string {
	tempFilename := uuid.NewString()

	return fmt.Sprintf("%s/.temp.%012d-%d.bin.%s", c.path, offset/c.chunkSize, c.chunkSize, tempFilename)
}

func (c *CachedFileObjectProvider) makeChunkFilename(offset int64) string {
	return fmt.Sprintf("%s/%012d-%d.bin", c.path, offset/c.chunkSize, c.chunkSize)
}

func (c *CachedFileObjectProvider) writeChunkToCache(ctx context.Context, offset int64, chunkPath string, bytes []byte) {
	writeTimer := cacheWriteTimerFactory.Begin()

	tempPath := c.makeTempChunkFilename(offset)

	if err := os.WriteFile(tempPath, bytes, cacheFilePermissions); err != nil {
		zap.L().Error("failed to write temp cache file",
			zap.String("tempPath", tempPath),
			zap.String("chunkPath", chunkPath),
			zap.Int64("offset", offset),
			zap.Int("length", len(bytes)),
			zap.Error(err),
		)

		return
	}

	if err := moveWithoutReplace(tempPath, chunkPath); err != nil {
		zap.L().Error("failed to rename temp file",
			zap.String("tempPath", tempPath),
			zap.String("chunkPath", chunkPath),
			zap.Int64("offset", offset),
			zap.Int("length", len(bytes)),
			zap.Error(err),
		)

		return
	}

	writeTimer.End(ctx, int64(len(bytes)))
}

func (c *CachedFileObjectProvider) writeFullFileToCache(ctx context.Context, b []byte) {
	timer := cacheWriteTimerFactory.Begin()

	tempPath := c.tempFullFilename()

	if err := os.WriteFile(tempPath, b, cacheFilePermissions); err != nil {
		zap.L().Error("failed to write temp cache file",
			zap.String("path", tempPath),
			zap.Int("length", len(b)),
			zap.Error(err),
		)

		return
	}

	finalPath := c.fullFilename()
	if err := moveWithoutReplace(tempPath, finalPath); err != nil {
		zap.L().Error("failed to rename temp file",
			zap.String("tempPath", tempPath),
			zap.String("filePath", finalPath),
			zap.Int("length", len(b)),
			zap.Error(err),
		)

		return
	}

	timer.End(ctx, int64(len(b)))
}

func (c *CachedFileObjectProvider) readAtFromCache(chunkPath string, buff []byte) (int, error) {
	var fp *os.File
	fp, err := os.Open(chunkPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open file: %w", err)
	}

	defer cleanup("failed to close chunk", fp.Close)

	count, err := fp.ReadAt(buff, 0) // offset is in the filename
	if ignoreEOF(err) != nil {
		return 0, fmt.Errorf("failed to read from chunk: %w", err)
	}

	return count, err // return `err` in case it's io.EOF
}

func (c *CachedFileObjectProvider) copyFullFileFromCache(ctx context.Context, dst io.Writer) (int64, bool) {
	cachedRead := cacheReadTimerFactory.Begin()

	path := c.fullFilename()

	var fp *os.File
	fp, err := os.Open(path)
	if err != nil {
		if !os.IsNotExist(err) {
			zap.L().Error("failed to open full cached file",
				zap.String("path", path),
				zap.Error(err))
		}
		return 0, false
	}

	defer cleanup("failed to close full cached file", fp.Close)

	count, err := io.Copy(dst, fp)
	if ignoreEOF(err) != nil {
		zap.L().Error("failed to read full cached file",
			zap.String("path", path),
			zap.Error(err))
		return 0, false
	}

	cachedRead.End(ctx, count)
	return count, true
}

const (
	kilobyte = 1024
	megabyte = 1024 * kilobyte
)

func (c *CachedFileObjectProvider) readAndCacheFullRemoteFile(ctx context.Context, dst io.Writer) (int64, error) {
	// This is semi-arbitrary. this code path is called for files that tend to be less than 1 MB (headers, metadata, etc),
	// so 2 MB allows us to read the file without needing to allocate more memory, with some room for growth. If the
	// file is larger than 2 MB, the buffer will grow, it just won't be as efficient WRT memory allocations.
	const writeToInitialBufferSize = 2 * megabyte

	writer := bytes.NewBuffer(make([]byte, 0, writeToInitialBufferSize))

	if _, err := c.inner.WriteTo(ctx, writer); ignoreEOF(err) != nil {
		return 0, err
	}

	go func() {
		c.writeFullFileToCache(context.WithoutCancel(ctx), writer.Bytes())
	}()

	written, err := dst.Write(writer.Bytes())
	return int64(written), err
}

func cleanup(msg string, close func() error) {
	if err := close(); err != nil {
		zap.L().Warn(msg, zap.Error(err))
	}
}

func ignoreEOF(err error) error {
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

// moveWithoutReplace tries to rename a file but will not replace the target if it already exists.
// If the file already exists, the file will be deleted.
func moveWithoutReplace(oldPath, newPath string) error {
	defer func() {
		if err := os.Remove(oldPath); err != nil {
			zap.L().Warn("failed to remove existing file", zap.Error(err))
		}
	}()

	if err := os.Link(oldPath, newPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			// Someone else created newPath first. Treat as success.
			return nil
		}

		return err
	}

	return nil
}

func safelyRemoveFile(path string) {
	if err := os.Remove(path); ignoreFileMissingError(err) != nil {
		zap.L().Warn("failed to remove file",
			zap.String("path", path),
			zap.Error(err))
	}
}

func recordError(span trace.Span, err error) {
	if err == nil {
		return
	}

	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}
