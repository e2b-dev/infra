package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/units"
)

type partUploader interface {
	Start(ctx context.Context) error
	UploadPart(ctx context.Context, partIndex int, data ...[]byte) error
	Complete(ctx context.Context) error
	Close() error
}

const (
	// cloudMinPartSizeMB is the smallest non-final multipart part both S3 and
	// the GCS XML API accept (5 MiB). Smaller configured values would fail with
	// EntityTooSmall at CompleteMultipartUpload, after all bytes are shipped.
	cloudMinPartSizeMB = 5
	// cloudMaxParts bounds the part count; S3 and the GCS XML API cap multipart
	// uploads at 10,000 parts. Kept below the hard cap because parts are sized
	// by *compressed* bytes and incompressible frames can expand slightly past
	// the uncompressed file size this bound is computed from.
	cloudMaxParts = 9000
)

// clampCloudMinPartSize resolves the configured minimum part size against the
// multipart limits shared by S3 and the GCS XML API: at least 5 MiB per
// non-final part, and large enough that even an incompressible file of
// fileSize bytes stays under the 10,000-part cap.
func clampCloudMinPartSize(cfg CompressConfig, fileSize int64) CompressConfig {
	minMB := units.BytesToMB(cfg.MinPartSize()) // resolves the <= 0 default (50 MB)
	cfg.MinPartSizeMB = int(max(minMB, cloudMinPartSizeMB, units.BytesToMB(fileSize)/cloudMaxParts+1))

	return cfg
}

// storeFileCompressed streams localPath through compressStream into a
// provider-specific multipart upload. It owns the shared recipe — open, stat,
// stamp the uncompressed size into the object metadata (multipart APIs only
// accept metadata at initiate time) — so providers supply just the uploader.
func storeFileCompressed(ctx context.Context, localPath string, cfg CompressConfig, maxUploadConcurrency int, putOpts PutOptions, newUploader func(metadata ObjectMetadata) (partUploader, error)) (*FullFrameTable, [32]byte, error) {
	file, err := os.Open(localPath)
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("failed to open local file %s: %w", localPath, err)
	}
	defer file.Close()

	fi, err := file.Stat()
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("failed to stat local file %s: %w", localPath, err)
	}

	cfg = clampCloudMinPartSize(cfg, fi.Size())

	uploader, err := newUploader(putOpts.Metadata.WithUncompressedSize(fi.Size()))
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("failed to create multipart uploader: %w", err)
	}

	return compressStream(ctx, file, cfg, uploader, maxUploadConcurrency, putOpts.FrameSink)
}

type memPartUploader struct {
	mu    sync.Mutex
	parts map[int][]byte
}

func (m *memPartUploader) Start(context.Context) error {
	m.parts = make(map[int][]byte)

	return nil
}

func (m *memPartUploader) UploadPart(_ context.Context, partIndex int, data ...[]byte) error {
	var buf bytes.Buffer
	for _, d := range data {
		buf.Write(d)
	}
	m.mu.Lock()
	m.parts[partIndex] = buf.Bytes()
	m.mu.Unlock()

	return nil
}

func (m *memPartUploader) Complete(context.Context) error { return nil }
func (m *memPartUploader) Close() error                   { return nil }

func (m *memPartUploader) Assemble() []byte {
	keys := make([]int, 0, len(m.parts))
	for k := range m.parts {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	var buf bytes.Buffer
	for _, k := range keys {
		buf.Write(m.parts[k])
	}

	return buf.Bytes()
}

// inputBufPool is shared across all uploads so frame-sized buffers (almost
// always DefaultCompressFrameSize) are reused between streams instead of being
// reallocated per call. See buffer_pool.go for the buffer lifecycle.
var inputBufPool = newBufferPool()

type frame struct {
	uncompressedSize int
	compressed       []byte
}

type part struct {
	index          int
	frames         []*frame
	compressedSize atomic.Int64
	compress       *errgroup.Group
}

func newPart(index int, parentCtx context.Context, workers int) (*part, context.Context) {
	p := &part{index: index}
	var ctx context.Context
	p.compress, ctx = errgroup.WithContext(parentCtx)
	p.compress.SetLimit(workers)

	return p, ctx
}

func (p *part) addFrame(ctx context.Context, buf inputBuf, n int, pool *sync.Pool) {
	frameInPart := &frame{uncompressedSize: n}
	p.frames = append(p.frames, frameInPart)
	uncompressedData := buf.Bytes()[:n]

	p.compress.Go(func() error {
		defer buf.Free()
		if err := ctx.Err(); err != nil {
			return err
		}
		c := pool.Get().(compressor)
		out, err := c.compress(uncompressedData)
		pool.Put(c)
		if err != nil {
			return err
		}
		frameInPart.compressed = out
		p.compressedSize.Add(int64(len(out)))

		return nil
	})
}

func compressStream(ctx context.Context, in io.Reader, cfg CompressConfig, uploader partUploader, maxUploadConcurrency int, sink FrameSink) (*FullFrameTable, [32]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if err := uploader.Start(ctx); err != nil {
		return nil, [32]byte{}, fmt.Errorf("start upload: %w", err)
	}
	defer uploader.Close()

	// The read loop goroutine holds one slot for the duration of the stream;
	// at least one additional slot is required for uploaders to make progress.
	if maxUploadConcurrency < 1 {
		maxUploadConcurrency = 1
	}
	work, workCtx := errgroup.WithContext(ctx)
	work.SetLimit(maxUploadConcurrency + 1)

	// Start the read loop.
	q := make(chan *part, 2)
	hasher := sha256.New()
	work.Go(func() error {
		defer close(q)

		return readLoop(workCtx, in, cfg, hasher, q)
	})

	// Upload loop.
	var frameSizes []FrameSize
	var cOffset int64
	var loopErr error
	for p := range q {
		if err := p.compress.Wait(); err != nil {
			loopErr = fmt.Errorf("compress frames: %w", err)
			cancel()

			break
		}

		var compressed [][]byte
		for _, f := range p.frames {
			frameSizes = append(frameSizes, FrameSize{U: int32(f.uncompressedSize), C: int32(len(f.compressed))})
			compressed = append(compressed, f.compressed)
			if sink != nil {
				sink(ctx, cOffset, f.compressed)
			}
			cOffset += int64(len(f.compressed))
		}

		pi := p.index
		work.Go(func() error {
			return uploader.UploadPart(workCtx, pi, compressed...)
		})
	}

	// Drain q so the read loop can exit and close it, then wait for all
	// in-flight uploads to finish before the deferred uploader.Close().
	for range q { //nolint:revive // intentional drain
	}
	workErr := work.Wait()

	if err := errors.Join(loopErr, workErr); err != nil {
		return nil, [32]byte{}, err
	}

	// Zero-byte input produces no parts, but S3 and the GCS XML API both
	// refuse to complete a multipart upload with zero parts — ship a single
	// empty final part so empty files still store successfully.
	if len(frameSizes) == 0 {
		if err := uploader.UploadPart(ctx, 1); err != nil {
			return nil, [32]byte{}, fmt.Errorf("upload empty part: %w", err)
		}
	}

	if err := uploader.Complete(ctx); err != nil {
		return nil, [32]byte{}, fmt.Errorf("complete upload: %w", err)
	}

	ft := NewFullFrameTable(cfg.CompressionType(), frameSizes)

	return ft, sum256(hasher), nil
}

func readLoop(ctx context.Context, in io.Reader, cfg CompressConfig, hasher io.Writer, q chan<- *part) error {
	compressors, err := newCompressorPool(cfg)
	if err != nil {
		return err
	}

	frameSize := cfg.FrameSize()
	minPartSize := cfg.MinPartSize()
	workers := max(cfg.FrameEncodeWorkers, 1)
	p, compressCtx := newPart(1, ctx, workers)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		buf := inputBufPool.Get(frameSize)
		data := buf.Bytes()
		n, err := io.ReadFull(in, data)

		eof := errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
		if err != nil && !eof {
			buf.Free()

			return fmt.Errorf("read frame: %w", err)
		}

		if n > 0 {
			hasher.Write(data[:n])
			p.addFrame(compressCtx, buf, n, compressors)
		} else {
			buf.Free()
		}

		if eof {
			if len(p.frames) > 0 {
				select {
				case q <- p:
				case <-ctx.Done():
					return ctx.Err()
				}
			}

			return nil
		}

		if p.compressedSize.Load() >= minPartSize {
			select {
			case q <- p:
			case <-ctx.Done():
				return ctx.Err()
			}

			p, compressCtx = newPart(p.index+1, ctx, workers)
		}
	}
}
