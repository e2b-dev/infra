package internal

import (
	"context"
	"errors"
	"io"

	"github.com/e2b-dev/infra/packages/block-device/internal/backend"
	"github.com/e2b-dev/infra/packages/block-device/internal/block"
	"github.com/e2b-dev/infra/packages/block-device/internal/frontend"
)

type Device struct {
	source   io.ReaderAt
	cache    block.Device
	frontend *frontend.UnixDevice
}

func NewDevice(
	socketPath,
	bucketName,
	bucketPath string,
	size int64,
) (*Device, error) {
	ctx := context.Background()

	frontend := frontend.NewUnixDevice(socketPath)

	mem := backend.NewMemoryStorage(size)

	gcp, err := backend.NewGCS(
		ctx,
		bucketName,
		bucketPath,
		size,
	)
	if err != nil {
		return nil, err
	}

	dev := &Device{
		source:   gcp,
		cache:    mem,
		frontend: frontend,
	}

	return dev, nil
}

func (d *Device) ReadAt(p []byte, off int64) (n int, err error) {
	n, err = d.cache.ReadAt(p, off)

	var bytesError *block.ErrBytesNotAvailable

	if errors.As(err, &bytesError) {
		n, err = d.source.ReadAt(p, off)
		if err != nil {
			return n, err
		}
	}

	if err != nil {
		return n, err
	}

	return n, nil
}

func (d *Device) WriteAt(p []byte, off int64) (n int, err error) {
	return d.cache.WriteAt(p, off)
}
