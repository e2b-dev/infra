package gcs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"time"

	"cloud.google.com/go/storage"
	"github.com/googleapis/gax-go/v2"
)

const (
	readTimeout       = 10 * time.Second
	operationTimeout  = 5 * time.Second
	bufferSize        = 2 << 21
	initialBackoff    = 10 * time.Millisecond
	maxBackoff        = 10 * time.Second
	backoffMultiplier = 2
	maxAttempts       = 10
)

type Object struct {
	object *storage.ObjectHandle
	ctx    context.Context
}

func NewObject(ctx context.Context, bucket *storage.BucketHandle, objectPath string) *Object {
	obj := bucket.Object(objectPath).Retryer(
		storage.WithMaxAttempts(maxAttempts),
		storage.WithBackoff(gax.Backoff{
			Initial:    initialBackoff,
			Max:        maxBackoff,
			Multiplier: backoffMultiplier,
		}),
		storage.WithPolicy(storage.RetryAlways),
	)

	return &Object{
		object: obj,
		ctx:    ctx,
	}
}

func (o *Object) WriteTo(dst io.Writer) (int64, error) {
	ctx, cancel := context.WithTimeout(o.ctx, readTimeout)
	defer cancel()

	reader, err := o.object.NewReader(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to create GCS reader: %w", err)
	}

	defer reader.Close()

	b := make([]byte, bufferSize)

	n, err := io.CopyBuffer(dst, reader, b)
	if err != nil {
		return n, fmt.Errorf("failed to copy GCS object to writer: %w", err)
	}

	return n, nil
}

func (o *Object) ReadFrom(src io.Reader) (int64, error) {
	w := o.object.NewWriter(o.ctx)

	n, err := io.Copy(w, src)
	if err != nil && !errors.Is(err, io.EOF) {
		return n, fmt.Errorf("failed to copy buffer to storage: %w", err)
	}

	err = w.Close()
	if err != nil {
		return n, fmt.Errorf("failed to close GCS writer: %w", err)
	}

	return n, nil
}

func (o *Object) Copy(ctx context.Context, to *Object) error {
	fromPath := fmt.Sprintf("gs://%s/%s", o.object.BucketName(), o.object.ObjectName())
	toPath := fmt.Sprintf("gs://%s/%s", to.object.BucketName(), to.object.ObjectName())

	cmd := exec.CommandContext(
		ctx,
		"gcloud",
		"storage",
		"cp",
		"--verbosity",
		"error",
		fromPath,
		toPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to copy GCS object: %w\n%s", err, string(output))
	}

	return nil
}

func (o *Object) UploadWithCli(ctx context.Context, path string) error {
	cmd := exec.CommandContext(
		ctx,
		"gcloud",
		"storage",
		"cp",
		"--verbosity",
		"error",
		path,
		fmt.Sprintf("gs://%s/%s", o.object.BucketName(), o.object.ObjectName()),
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to upload file to GCS: %w\n%s", err, string(output))
	}

	return nil
}

func (o *Object) ReadAt(b []byte, off int64) (n int, err error) {
	ctx, cancel := context.WithTimeout(o.ctx, readTimeout)
	defer cancel()

	// The file should not be gzip compressed
	reader, err := o.object.NewRangeReader(ctx, off, int64(len(b)))
	if err != nil {
		return 0, fmt.Errorf("failed to create GCS reader: %w", err)
	}

	defer reader.Close()

	for reader.Remain() > 0 {
		nr, readErr := reader.Read(b[n:])
		n += nr

		if readErr == nil {
			continue
		}

		if errors.Is(readErr, io.EOF) {
			break
		}

		return n, fmt.Errorf("failed to read from GCS object: %w", readErr)
	}

	return n, nil
}

func (o *Object) Size() (int64, error) {
	ctx, cancel := context.WithTimeout(o.ctx, operationTimeout)
	defer cancel()

	attrs, err := o.object.Attrs(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to get GCS object (%s) attributes: %w", o.object.ObjectName(), err)
	}

	return attrs.Size, nil
}

func (o *Object) Delete() error {
	ctx, cancel := context.WithTimeout(o.ctx, operationTimeout)
	defer cancel()

	return o.object.Delete(ctx)
}
