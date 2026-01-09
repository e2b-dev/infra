package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"slices"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
)

var experiments = map[string]map[string]experiment{
	"concurrent requests": {
		"1": &setConcurrentRequests{1},
		//"4": &setConcurrentRequests{4},
		//"8":  &setConcurrentRequests{8},
		//"16": &setConcurrentRequests{16},
		"32": &setConcurrentRequests{32},
		"64": &setConcurrentRequests{64},
	},
	"anywhere cache": {
		"uncached": nil,
		//"cached":   &skipFirstRead{},
	},
	"shared buffer": {
		//"shared buffer": &sharedBuffer{},
		"fresh buffer": &alwaysNewBuffer{},
	},
	"grpc metrics": {
		"disabled": &grpcOption{option.WithTelemetryDisabled()},
	},
	"client metrics": {
		"disabled": &grpcOption{storage.WithDisabledClientMetrics()},
	},
	"grpc connection pool": {
		"default": nil,
		"1":       &grpcOption{option.WithGRPCConnectionPool(1)},
		"8":       &grpcOption{option.WithGRPCConnectionPool(4)},
		"16":      &grpcOption{option.WithGRPCConnectionPool(16)},
	},
	"grpc initial window size": {
		//"default": nil,
		//"4MB":  &grpcOption{option.WithGRPCDialOption(grpc.WithInitialWindowSize(4 * megabyte))},
		//"8MB":  &grpcOption{option.WithGRPCDialOption(grpc.WithInitialWindowSize(8 * megabyte))},
		"16MB": &grpcOption{option.WithGRPCDialOption(grpc.WithInitialWindowSize(16 * megabyte))},
	},
	"grpc initial conn window size": {
		"default": nil,
		"16MB":    &grpcOption{option.WithGRPCDialOption(grpc.WithInitialConnWindowSize(16 * megabyte))},
		"32MB":    &grpcOption{option.WithGRPCDialOption(grpc.WithInitialConnWindowSize(32 * megabyte))},
	},
	"service config": {
		//"disabled": &grpcOption{option.WithGRPCDialOption(grpc.WithDisableServiceConfig())},  // breaks the test
		"enabled": nil,
	},
	"read method": {
		//"http":      newGoogleCloudHTTPClient(),
		"grpc": newGoogleCloudGRPCClient(),
		//"bidi grpc": newGoogleCloudGRPCClient(experimental.WithGRPCBidiReads()), // "The BidiReadObject RPC is not yet available for general use"
	},
}

type experiment interface {
	setup(ctx context.Context, o *options) error
	teardown(ctx context.Context, o *options) error
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
			if err := e.exp.setup(ctx, &o); err != nil {
				errs = append(errs, fmt.Errorf("failed to setup %q: %w", e, err))
			}
		}
	}

	for _, m := range o.readMiddleware {
		o.readMethod = m(o.readMethod)
	}

	var err error
	if o.client, err = storage.NewGRPCClient(ctx, o.clientOptions...); err != nil {
		return nil, fmt.Errorf("failed to create storage client: %w", err)
	}

	return &o, errors.Join(errs...)
}

func (s scenario) teardown(ctx context.Context, o *options) error {
	var errs []error

	for name, e := range s.elements {
		if e.exp != nil {
			if err := e.exp.teardown(ctx, o); err != nil {
				errs = append(errs, fmt.Errorf("failed to teardown %q: %w", name, err))
			}
		}
	}

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

func (s *setConcurrentRequests) setup(_ context.Context, o *options) error {
	o.concurrentRequests = s.concurrentRequests

	return nil
}

func (s *setConcurrentRequests) teardown(_ context.Context, _ *options) error { return nil }

var _ experiment = (*setConcurrentRequests)(nil)

type googleCloudGRPCClient struct {
	opts []option.ClientOption
}

var _ experiment = (*googleCloudGRPCClient)(nil)

func newGoogleCloudGRPCClient(opts ...option.ClientOption) *googleCloudGRPCClient {
	return &googleCloudGRPCClient{opts: opts}
}

func (g *googleCloudGRPCClient) setup(ctx context.Context, o *options) error {
	o.readMethod = g.read(o)

	return nil
}

func (g *googleCloudGRPCClient) teardown(ctx context.Context, o *options) error {
	return nil
}

func (r *googleCloudGRPCClient) read(o *options) readMethod {
	return func(ctx context.Context, info readInfo) (time.Duration, error) {
		now := time.Now()

		rc, err := o.client.Bucket(o.bucket).Object(info.path).NewRangeReader(ctx, info.offset, o.chunkSize)
		if err != nil {
			return 0, fmt.Errorf("failed to create reader: %w", err)
		}
		defer safeClose(rc)

		var bytesRead int64
		for bytesRead < o.chunkSize {
			n, err := rc.Read(info.buffer[bytesRead:])
			if err != nil {
				if err == io.EOF {
					break
				}
				return 0, fmt.Errorf("failed to read from gcs: %w", err)
			}
			bytesRead += int64(n)
		}
		if bytesRead != o.chunkSize {
			return 0, fmt.Errorf("unexpected number of bytes read: %d", bytesRead)
		}

		return time.Since(now), nil
	}
}

type googleCloudHTTPClient struct {
	rand   *rand.Rand
	client *storage.Client
}

func newGoogleCloudHTTPClient() *googleCloudHTTPClient {
	source := rand.NewSource(time.Now().UnixNano())

	return &googleCloudHTTPClient{
		rand: rand.New(source),
	}
}

func (r *googleCloudHTTPClient) setup(ctx context.Context, p *options) error {
	var err error
	if r.client, err = storage.NewClient(ctx); err != nil {
		return fmt.Errorf("failed to create storage client: %w", err)
	}

	p.readMethod = r.read(p)

	return nil
}

func (r *googleCloudHTTPClient) teardown(_ context.Context, p *options) error {
	p.readMethod = nil

	if err := r.client.Close(); err != nil {
		return fmt.Errorf("failed to close storage client: %w", err)
	}

	r.client = nil

	return nil
}

func (r *googleCloudHTTPClient) read(p *options) readMethod {
	return func(ctx context.Context, fileInfo readInfo) (time.Duration, error) {
		now := time.Now()

		rc, err := r.client.Bucket(p.bucket).Object(fileInfo.path).NewRangeReader(ctx, fileInfo.offset, p.chunkSize)
		if err != nil {
			return 0, fmt.Errorf("failed to create reader: %w", err)
		}
		defer safeClose(rc)

		var bytesRead int64
		for bytesRead < p.chunkSize {
			n, err := rc.Read(fileInfo.buffer[bytesRead:])
			if err != nil {
				if err == io.EOF {
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

var _ experiment = (*googleCloudHTTPClient)(nil)

type skipFirstRead struct{}

var _ experiment = (*skipFirstRead)(nil)

func (s skipFirstRead) setup(_ context.Context, p *options) error {
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

func (s skipFirstRead) teardown(_ context.Context, _ *options) error {
	return nil
}

type sharedBuffer struct {
	buffer []byte
}

var _ experiment = (*sharedBuffer)(nil)

func (s *sharedBuffer) setup(_ context.Context, o *options) error {
	s.buffer = make([]byte, o.chunkSize)

	o.makeBuffer = func() []byte {
		return s.buffer
	}

	return nil
}

func (s *sharedBuffer) teardown(_ context.Context, _ *options) error {
	return nil
}

type alwaysNewBuffer struct{}

var _ experiment = (*alwaysNewBuffer)(nil)

func (s alwaysNewBuffer) setup(_ context.Context, o *options) error {
	o.makeBuffer = func() []byte {
		return make([]byte, o.chunkSize)
	}

	return nil
}

func (s alwaysNewBuffer) teardown(ctx context.Context, p *options) error {
	return nil
}

type grpcOption struct {
	opt option.ClientOption
}

var _ experiment = (*grpcOption)(nil)

func (g grpcOption) setup(ctx context.Context, o *options) error {
	o.clientOptions = append(o.clientOptions, g.opt)
	return nil
}

func (g grpcOption) teardown(ctx context.Context, o *options) error {
	return nil
}
