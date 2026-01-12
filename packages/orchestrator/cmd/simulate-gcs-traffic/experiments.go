package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
)

type options struct {
	bucket             string
	chunkSize          int64
	client             *storage.Client
	concurrentRequests int

	makeBuffer     bufferMethod
	readMethod     readMethod
	readMiddleware []func(readMethod) readMethod

	clientFactory func(ctx context.Context, opts ...option.ClientOption) (*storage.Client, error)
	clientOptions []option.ClientOption
}

var experiments = map[string]map[string]experiment{
	"concurrent requests": {
		"1":  &setConcurrentRequests{1},
		"4":  &setConcurrentRequests{4},
		"12": &setConcurrentRequests{8},
		// "16": &setConcurrentRequests{16},
		// "32": &setConcurrentRequests{32},
		// "48": &setConcurrentRequests{48},
		// "64": &setConcurrentRequests{64},
		//"128": &setConcurrentRequests{128},
		// "256": &setConcurrentRequests{256},
	},
	"anywhere cache": {
		"uncached": nil,
		// "cached":   &skipFirstRead{},
	},
	"shared buffer": {
		// "shared buffer": &sharedBuffer{},
		"fresh buffer": &alwaysNewBuffer{},
	},
	"grpc metrics": {
		"disabled": &googleOption{option.WithTelemetryDisabled()},
	},
	"client metrics": {
		"disabled": &googleOption{storage.WithDisabledClientMetrics()},
	},
	"grpc connection pool": {
		// "default": nil,
		"1":  &googleOption{option.WithGRPCConnectionPool(1)},
		"4":  &googleOption{option.WithGRPCConnectionPool(4)},
		"8":  &googleOption{option.WithGRPCConnectionPool(4)},
		"16": &googleOption{option.WithGRPCConnectionPool(16)},
	},
	"grpc initial window size": {
		"default": nil,
		"4MB":     &googleOption{option.WithGRPCDialOption(grpc.WithInitialWindowSize(4 * megabyte))},
		"8MB":     &googleOption{option.WithGRPCDialOption(grpc.WithInitialWindowSize(8 * megabyte))},
		"16MB":    &googleOption{option.WithGRPCDialOption(grpc.WithInitialWindowSize(16 * megabyte))},
		"32MB":    &googleOption{option.WithGRPCDialOption(grpc.WithInitialWindowSize(32 * megabyte))},
	},
	"grpc initial conn window size": {
		"default": nil,
		"16MB":    &googleOption{option.WithGRPCDialOption(grpc.WithInitialConnWindowSize(16 * megabyte))},
		"32MB":    &googleOption{option.WithGRPCDialOption(grpc.WithInitialConnWindowSize(32 * megabyte))},
	},
	"service config": {
		// "disabled": &googleOption{option.WithGRPCDialOption(grpc.WithDisableServiceConfig())},  // breaks the test
		"enabled": nil,
	},
	"client type": {
		"http": &createClientFactory{storage.NewClient},
		"grpc": &createClientFactory{storage.NewGRPCClient},
	},
	"read method": {
		"google storage": &googleStorageRangeRead{},
	},
}

type experiment interface {
	apply(ctx context.Context, o *options) error
}

type element struct {
	name string
	exp  experiment
}

type scenario struct {
	elements map[string]element
}

func (s scenario) setup(ctx context.Context, p *processor) (*options, error) {
	o := options{
		bucket:             p.bucket,
		chunkSize:          p.chunkSize,
		concurrentRequests: 1,
	}

	var errs []error

	for _, e := range s.elements {
		if e.exp != nil {
			if err := e.exp.apply(ctx, &o); err != nil {
				errs = append(errs, fmt.Errorf("failed to setup %q: %w", e, err))
			}
		}
	}

	for _, m := range o.readMiddleware {
		o.readMethod = m(o.readMethod)
	}

	var err error
	if o.client, err = o.clientFactory(ctx, o.clientOptions...); err != nil {
		return nil, fmt.Errorf("failed to create storage client: %w", err)
	}

	return &o, errors.Join(errs...)
}

func (s scenario) teardown(_ context.Context, o *options) error {
	var errs []error

	if err := o.client.Close(); err != nil {
		return fmt.Errorf("failed to close storage client: %w", err)
	}

	return errors.Join(errs...)
}

func (s scenario) Name() any {
	var keys []string
	for k := range s.elements {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	var values []string
	for _, k := range keys {
		values = append(values, fmt.Sprintf("%s=%s", k, s.elements[k].name))
	}

	return strings.Join(values, "; ")
}

type setConcurrentRequests struct {
	concurrentRequests int
}

func (s *setConcurrentRequests) apply(_ context.Context, o *options) error {
	o.concurrentRequests = s.concurrentRequests

	return nil
}

var _ experiment = (*setConcurrentRequests)(nil)

type skipFirstRead struct{}

var _ experiment = (*skipFirstRead)(nil)

func (s skipFirstRead) apply(_ context.Context, p *options) error {
	p.readMiddleware = append(p.readMiddleware, s.middleware)

	return nil
}

func (s skipFirstRead) middleware(fn readMethod) readMethod {
	return func(ctx context.Context, info readInfo) (time.Duration, error) {
		_, err := fn(ctx, info)
		if err != nil {
			return 0, fmt.Errorf("failed to make uncached gcs read: %w", err)
		}

		return fn(ctx, info)
	}
}

type sharedBuffer struct {
	buffer []byte
}

var _ experiment = (*sharedBuffer)(nil)

func (s *sharedBuffer) apply(_ context.Context, o *options) error {
	s.buffer = make([]byte, o.chunkSize)

	o.makeBuffer = func() []byte {
		return s.buffer
	}

	return nil
}

type alwaysNewBuffer struct{}

var _ experiment = (*alwaysNewBuffer)(nil)

func (s alwaysNewBuffer) apply(_ context.Context, o *options) error {
	o.makeBuffer = func() []byte {
		return make([]byte, o.chunkSize)
	}

	return nil
}

type googleOption struct {
	opt option.ClientOption
}

var _ experiment = (*googleOption)(nil)

func (g googleOption) apply(_ context.Context, o *options) error {
	o.clientOptions = append(o.clientOptions, g.opt)

	return nil
}

type googleStorageRangeRead struct{}

var _ experiment = (*googleStorageRangeRead)(nil)

func (g googleStorageRangeRead) apply(_ context.Context, o *options) error {
	o.readMethod = g.read(o)

	return nil
}

func (g googleStorageRangeRead) read(p *options) readMethod {
	return func(ctx context.Context, fileInfo readInfo) (time.Duration, error) {
		now := time.Now()

		rc, err := p.client.Bucket(p.bucket).Object(fileInfo.path).NewRangeReader(ctx, fileInfo.offset, p.chunkSize)
		if err != nil {
			return 0, fmt.Errorf("failed to create reader: %w", err)
		}
		defer safeClose(rc)

		var bytesRead int64
		for bytesRead < p.chunkSize {
			n, err := rc.Read(fileInfo.buffer[bytesRead:])
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}

				return 0, fmt.Errorf("failed to read from gcs: %w", err)
			}
			bytesRead += int64(n)
		}
		if bytesRead != p.chunkSize {
			return 0, fmt.Errorf("unexpected number of bytes read: %d", bytesRead)
		}

		return time.Since(now), nil
	}
}

type createClientFactory struct {
	factory func(ctx context.Context, opts ...option.ClientOption) (*storage.Client, error)
}

func (c createClientFactory) apply(_ context.Context, o *options) error {
	o.clientFactory = c.factory

	return nil
}

var _ experiment = (*createClientFactory)(nil)
