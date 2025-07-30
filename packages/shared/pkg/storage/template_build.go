package storage

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/klauspost/pgzip"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	headers "github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const useCompression = false

var tracer = otel.Tracer("shared.pkg.storage")

type TemplateBuild struct {
	files       TemplateFiles
	persistence StorageProvider

	memfileHeader *headers.Header
	rootfsHeader  *headers.Header
}

func NewTemplateBuild(memfileHeader *headers.Header, rootfsHeader *headers.Header, persistence StorageProvider, files TemplateFiles) *TemplateBuild {
	return &TemplateBuild{
		persistence: persistence,
		files:       files,

		memfileHeader: memfileHeader,
		rootfsHeader:  rootfsHeader,
	}
}

func (t *TemplateBuild) Remove(ctx context.Context) error {
	err := t.persistence.DeleteObjectsWithPrefix(ctx, t.files.StorageDir())
	if err != nil {
		return fmt.Errorf("error when removing template build '%s': %w", t.files.StorageDir(), err)
	}

	return nil
}

func (t *TemplateBuild) uploadMemfileHeader(ctx context.Context, h *headers.Header) error {
	ctx, span := tracer.Start(ctx, "TemplateBuild.uploadMemfileHeader")
	defer span.End()

	object, err := t.persistence.OpenObject(ctx, t.files.StorageMemfileHeaderPath())
	if err != nil {
		return err
	}

	serialized, err := headers.Serialize(h.Metadata, h.Mapping)
	if err != nil {
		return fmt.Errorf("error when serializing memfile header: %w", err)
	}

	_, err = object.ReadFrom(serialized)
	if err != nil {
		return fmt.Errorf("error when uploading memfile header: %w", err)
	}

	return nil
}

func (t *TemplateBuild) uploadPath(ctx context.Context, source, dest string) error {
	ctx, span := tracer.Start(ctx, "TemplateBuild.uploadPath",
		trace.WithAttributes(attribute.Int64("file-size", getSize(source))))
	defer span.End()

	object, err := t.persistence.OpenObject(ctx, dest)
	if err != nil {
		return err
	}

	err = object.WriteFromFileSystem(source)
	if err != nil {
		return fmt.Errorf("error when uploading path: %w", err)
	}

	return nil
}

func getSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		zap.L().Warn("failed to get size of file", zap.Error(err))
		return -1
	}

	return info.Size()
}

func reportError(msg string, fn func() error) {
	if err := fn(); err != nil {
		zap.L().Warn(msg, zap.Error(err))
	}
}

func (t *TemplateBuild) compressFile(ctx context.Context, path string) (string, func(), error) {
	if !useCompression {
		return path, func() {}, nil
	}

	ctx, span := tracer.Start(ctx, "TemplateBuild.compressFile")
	defer span.End()

	source, err := os.Open(path)
	if err != nil {
		return "", nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer reportError("failed to close source", source.Close)

	destination, err := os.CreateTemp("", "")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	defer reportError("failed to close destination", destination.Close)

	compressed := pgzip.NewWriter(destination)
	span.SetAttributes(attribute.String("algorithm", "pgzip"))
	defer reportError("failed to close compressor", compressed.Close)

	if _, err = io.Copy(compressed, source); err != nil {
		return "", nil, fmt.Errorf("failed to compress file: %w", err)
	}

	span.SetAttributes(
		attribute.Int64("original_size", getSize(path)),
		attribute.Int64("compressed_size", getSize(destination.Name())),
	)

	return destination.Name(), func() {
		reportError("failed to remove temp file", func() error {
			return os.Remove(destination.Name())
		})
	}, nil
}

func (t *TemplateBuild) uploadRootfsHeader(ctx context.Context, h *headers.Header) error {
	ctx, span := tracer.Start(ctx, "TemplateBuild.uploadRootfsHeader")
	defer span.End()

	object, err := t.persistence.OpenObject(ctx, t.files.StorageRootfsHeaderPath())
	if err != nil {
		return err
	}

	serialized, err := headers.Serialize(h.Metadata, h.Mapping)
	if err != nil {
		return fmt.Errorf("error when serializing memfile header: %w", err)
	}

	_, err = object.ReadFrom(serialized)
	if err != nil {
		return fmt.Errorf("error when uploading memfile header: %w", err)
	}

	return nil
}

// Snap-file is small enough so we don't use composite upload.
func (t *TemplateBuild) uploadSnapfile(ctx context.Context, snapfile io.Reader) error {
	ctx, span := tracer.Start(ctx, "TemplateBuild.uploadSnapfile")
	defer span.End()

	object, err := t.persistence.OpenObject(ctx, t.files.StorageSnapfilePath())
	if err != nil {
		return err
	}

	n, err := object.ReadFrom(snapfile)
	if err != nil {
		return fmt.Errorf("error when uploading snapfile (%d bytes): %w", n, err)
	}

	return nil
}

func (t *TemplateBuild) Upload(ctx context.Context, snapfilePath string, memfilePath *string, rootfsPath *string) chan error {
	ctx, span := tracer.Start(ctx, "TemplateBuild.Upload")
	defer span.End()

	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		if t.rootfsHeader == nil {
			return nil
		}

		ctx, span := tracer.Start(ctx, "root-fs-header")
		defer span.End()

		err := t.uploadRootfsHeader(ctx, t.rootfsHeader)
		if err != nil {
			return err
		}

		return nil
	})

	eg.Go(func() error {
		if rootfsPath == nil {
			return nil
		}

		ctx, span := tracer.Start(ctx, "root-fs", trace.WithAttributes(
			attribute.String("path", *rootfsPath),
			attribute.Int64("size", getSize(*rootfsPath)),
		))
		defer span.End()

		compressedPath, cleanup, err := t.compressFile(ctx, *rootfsPath)
		if err != nil {
			return fmt.Errorf("failed to compress rootfs: %w", err)
		}
		defer cleanup()

		if err = t.uploadPath(ctx, compressedPath, t.files.StorageRootfsPath()); err != nil {
			return fmt.Errorf("failed to compress rootfs: %w", err)
		}

		return nil
	})

	eg.Go(func() error {
		if t.memfileHeader == nil {
			return nil
		}

		ctx, span := tracer.Start(ctx, "memfile-header")
		defer span.End()

		if err := t.uploadMemfileHeader(ctx, t.memfileHeader); err != nil {
			return fmt.Errorf("failed to upload memfile header: %w", err)
		}

		return nil
	})

	eg.Go(func() error {
		if memfilePath == nil {
			return nil
		}

		ctx, span := tracer.Start(ctx, "memfile", trace.WithAttributes(
			attribute.String("path", *memfilePath),
			attribute.Int64("size", getSize(*memfilePath)),
		))
		defer span.End()

		compressedPath, cleanup, err := t.compressFile(ctx, *memfilePath)
		if err != nil {
			return fmt.Errorf("failed to compress rootfs: %w", err)
		}
		defer cleanup()

		if err = t.uploadPath(ctx, compressedPath, t.files.StorageMemfilePath()); err != nil {
			return fmt.Errorf("failed to upload memfile: %w", err)
		}

		return nil
	})

	eg.Go(func() error {
		ctx, span := tracer.Start(ctx, "snapfile", trace.WithAttributes(
			attribute.String("path", snapfilePath),
			attribute.Int64("size", getSize(snapfilePath)),
		))
		defer span.End()

		snapfile, err := os.Open(snapfilePath)
		if err != nil {
			return err
		}

		defer snapfile.Close()

		err = t.uploadSnapfile(ctx, snapfile)
		if err != nil {
			return err
		}

		return nil
	})

	done := make(chan error)

	go func() {
		done <- eg.Wait()
	}()

	return done
}
