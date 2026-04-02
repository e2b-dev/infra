package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/errgroup"
)

const (
	// DefaultCompressFrameSize is the default uncompressed size of each compression
	// frame (2 MiB). Overridable via CompressConfig.FrameSizeKB.
	// The last frame in a file may be shorter.
	//
	// The chunker fetches one frame at a time from storage on a cache miss.
	// Larger frame sizes mean more data cached per fetch (faster warm-up and
	// fewer GCS round-trips), but higher memory and I/O cost per miss.
	//
	// This MUST be multiple of every block/page size:
	//   - header.HugepageSize (2 MiB) — UFFD huge-page size, also used by prefetch
	//   - header.RootfsBlockSize (4 KiB) — NBD / rootfs block size
	DefaultCompressFrameSize = 2 * 1024 * 1024

	// File type identifiers for per-file-type compression targeting.
	FileTypeMemfile = "memfile"
	FileTypeRootfs  = "rootfs"

	// Use case identifiers for per-use-case compression targeting.
	UseCaseBuild = "build"
	UseCasePause = "pause"
)

// partUploader is the interface for uploading data in parts.
// Implementations exist for GCS multipart uploads and local file writes.
type partUploader interface {
	Start(ctx context.Context) error
	UploadPart(ctx context.Context, partIndex int, data ...[]byte) error
	Complete(ctx context.Context) error
	Close() error
}

// memPartUploader collects compressed parts in memory. Thread-safe.
// Useful for tests and benchmarks that need CompressStream output as bytes.
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

// Assemble returns the concatenated parts in index order.
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

type frame struct {
	uncompressedSize int
	compressed       []byte
}

type part struct {
	index          int
	frames         []*frame
	compressedSize atomic.Int64
	eg             *errgroup.Group
	readyToUpload  chan error
}

func newPart(index int, parentCtx context.Context, workers int) (p *part, ctx context.Context) {
	p = &part{index: index}
	p.eg, ctx = errgroup.WithContext(parentCtx)
	p.eg.SetLimit(workers)

	return p, ctx
}

func (p *part) addFrame(ctx context.Context, uncompressedData []byte, pool *sync.Pool) {
	if len(uncompressedData) == 0 {
		return
	}

	frameInPart := &frame{uncompressedSize: len(uncompressedData)}
	p.frames = append(p.frames, frameInPart)

	p.eg.Go(func() error {
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

func (p *part) submit(ctx context.Context, queue chan<- *part) {
	p.readyToUpload = make(chan error, 1)

	go func() {
		p.readyToUpload <- p.eg.Wait()
		close(p.readyToUpload)
	}()

	select {
	case queue <- p:
	case <-ctx.Done():
	}
}

// compressStream: read → compress (parallel) → emit metadata (ordered) → upload (concurrent).
func compressStream(ctx context.Context, in io.Reader, cfg *CompressConfig, uploader partUploader, maxUploadConcurrency int) (ft *FrameTable, checksum [32]byte, err error) { //nolint:unparam // callers in later PRs pass different values
	frameSize := cfg.FrameSize()
	targetPartSize := cfg.TargetPartSize()

	if err := uploader.Start(ctx); err != nil {
		return nil, [32]byte{}, fmt.Errorf("failed to start framed upload: %w", err)
	}
	defer uploader.Close()

	// for compression we create a pool per file since there are often enough
	// frames to justify pooling.
	compressors, err := newCompressorPool(cfg)
	if err != nil {
		return nil, [32]byte{}, err
	}
	hasher := sha256.New()

	ft = &FrameTable{compressionType: cfg.CompressionType()}

	ctx, cancel := context.WithCancel(ctx) // pipeline errors cancel the read loop
	defer cancel()

	q := make(chan *part, maxUploadConcurrency)
	var closeQ sync.Once
	defer closeQ.Do(func() { close(q) })

	uploadEG, uploadCtx := errgroup.WithContext(ctx)
	uploadEG.SetLimit(maxUploadConcurrency)

	var emitEG errgroup.Group
	emitEG.Go(func() error {
		for p := range q {
			select {
			case compressErr := <-p.readyToUpload:
				if compressErr != nil {
					cancel()

					return compressErr
				}
			case <-ctx.Done():
				return ctx.Err()
			}

			var compressed [][]byte
			for _, f := range p.frames {
				ft.Frames = append(ft.Frames, FrameSize{U: int32(f.uncompressedSize), C: int32(len(f.compressed))})
				compressed = append(compressed, f.compressed)
			}

			pi := p.index
			uploadEG.Go(func() error {
				return uploader.UploadPart(uploadCtx, pi, compressed...)
			})
		}

		return nil
	})

	part, compressCtx := newPart(1, ctx, cfg.FrameEncodeWorkers)
	for {
		if err := ctx.Err(); err != nil {
			return nil, [32]byte{}, err
		}

		buf := make([]byte, frameSize)
		n, err := io.ReadFull(in, buf)

		switch {
		case err == nil:
		case errors.Is(err, io.EOF):
		case errors.Is(err, io.ErrUnexpectedEOF):
			// fall through
		default:
			return nil, [32]byte{}, fmt.Errorf("read frame: %w", err)
		}

		if n > 0 {
			hasher.Write(buf[:n])
			part.addFrame(compressCtx, buf[:n], compressors)
		}

		if err != nil {
			break
		}

		if part.compressedSize.Load() >= targetPartSize {
			part.submit(ctx, q)
			part, compressCtx = newPart(part.index+1, ctx, cfg.FrameEncodeWorkers)
		}
	}

	if len(part.frames) > 0 {
		part.submit(ctx, q)
	}

	closeQ.Do(func() { close(q) })

	emitErr := emitEG.Wait()
	uploadErr := uploadEG.Wait()
	if err := errors.Join(emitErr, uploadErr); err != nil {
		return nil, [32]byte{}, err
	}

	if err := uploader.Complete(ctx); err != nil {
		return nil, [32]byte{}, fmt.Errorf("failed to finish uploading frames: %w", err)
	}

	copy(checksum[:], hasher.Sum(nil))

	return ft, checksum, nil
}
