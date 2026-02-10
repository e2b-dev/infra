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

var experiments = map[string]map[string]experiment{
	"concurrent requests": {
		"1": &setConcurrentRequests{1},
		// "4":  &setConcurrentRequests{4},
		// "12": &setConcurrentRequests{12},
		"16": &setConcurrentRequests{16},
		// "32": &setConcurrentRequests{32},
		// "48": &setConcurrentRequests{48},
		// "64": &setConcurrentRequests{64},
		// "128": &setConcurrentRequests{128},
		// "256": &setConcurrentRequests{256},
	},
	"cache warmup": {
		"no": nil,
		// "yes":   &skipReads{2, 50 * time.Millisecond},
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
		// "1": &googleOption{option.WithGRPCConnectionPool(1)},
		"4": &googleOption{option.WithGRPCConnectionPool(4)},
		// "8": &googleOption{option.WithGRPCConnectionPool(4)},
		// "16": &googleOption{option.WithGRPCConnectionPool(16)},
	},
	"grpc initial window size": {
		// "default": nil,
		"4MB": &googleOption{option.WithGRPCDialOption(grpc.WithInitialWindowSize(4 * megabyte))},
		// "8MB": &googleOption{option.WithGRPCDialOption(grpc.WithInitialWindowSize(8 * megabyte))},
		// "16MB":    &googleOption{option.WithGRPCDialOption(grpc.WithInitialWindowSize(16 * megabyte))},
		// "32MB":    &googleOption{option.WithGRPCDialOption(grpc.WithInitialWindowSize(32 * megabyte))},
	},
	"grpc initial conn window size": {
		// "default": nil,
		// "16MB":    &googleOption{option.WithGRPCDialOption(grpc.WithInitialConnWindowSize(16 * megabyte))},
		"32MB": &googleOption{option.WithGRPCDialOption(grpc.WithInitialConnWindowSize(32 * megabyte))},
	},
	"grpc read buffer": {
		"default": nil,
		// "1 MB":    &googleOption{option.WithGRPCDialOption(grpc.WithReadBufferSize(1 * megabyte))},
		// "4 MB":    &googleOption{option.WithGRPCDialOption(grpc.WithReadBufferSize(4 * megabyte))},
		// "16 MB":   &googleOption{option.WithGRPCDialOption(grpc.WithReadBufferSize(16 * megabyte))},
	},
	"compression": {
		"default": nil,
		// "gzip": &googleOption{option.WithGRPCDialOption(grpc.WithDefaultCallOptions(
		//	grpc.UseCompressor("gzip"),
		// ))},
	},
	"service config": {
		// "disabled": &googleOption{option.WithGRPCDialOption(grpc.WithDisableServiceConfig())},  // breaks the test
		"enabled": nil,
	},
	"client type": {
		// "http": &createClientFactory{storage.NewClient},
		"grpc": &createClientFactory{storage.NewGRPCClient},
	},
	"read method": {
		"google storage": &googleStorageRangeRead{},
	},
	"chunk size": {
		// "2MB": &setChunkSize{2 * megabyte},
		"4MB": &setChunkSize{4 * megabyte},
		// "8MB": &setChunkSize{8 * megabyte},
	},
	"read count": {
		// "150":  &setReadCount{150},
		"500": &setReadCount{500},
		// "1000": &setReadCount{1000},
	},
	"allow repeat reads": {
		// "disabled": &setAllowRepeatReads{false},
		"enabled": &setAllowRepeatReads{true},
	},
}

type options struct {
	bucket             string
	chunkSize          int64
	client             *storage.Client
	concurrentRequests int
	readCount          int
	skipCount          int
	allowRepeatReads   bool

	makeBuffer     bufferMethod
	readMethod     readMethod
	readMiddleware []func(readMethod) readMethod

	clientFactory func(ctx context.Context, opts ...option.ClientOption) (*storage.Client, error)
	clientOptions []option.ClientOption
}

func (o options) validate() error {
	var errs []error

	if o.bucket == "" {
		errs = append(errs, errors.New("bucket must be set"))
	}

	if o.readCount == 0 {
		errs = append(errs, errors.New("read-count must be set"))
	}

	if o.skipCount >= o.readCount {
		errs = append(errs, errors.New("skip-count must be less than read-count"))
	}

	if o.concurrentRequests < 1 {
		errs = append(errs, errors.New("concurrent-requests must be greater than 0"))
	}

	if o.chunkSize <= 0 {
		errs = append(errs, errors.New("chunk-size must be greater than 0"))
	}

	return errors.Join(errs...)
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
		concurrentRequests: 1,
		readCount:          100,
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

	if err := o.validate(); err != nil {
		return nil, fmt.Errorf("failed to validate options: %w", err)
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

	o.client = nil

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

type setChunkSize struct {
	chunkSize int64
}

func (s *setChunkSize) apply(_ context.Context, o *options) error {
	o.chunkSize = s.chunkSize

	return nil
}

var _ experiment = (*setChunkSize)(nil)

type setReadCount struct {
	readCount int
}

func (s *setReadCount) apply(_ context.Context, o *options) error {
	o.readCount = s.readCount

	return nil
}

var _ experiment = (*setReadCount)(nil)

type setSkipCount struct {
	skipCount int
}

func (s *setSkipCount) apply(_ context.Context, o *options) error {
	o.skipCount = s.skipCount

	return nil
}

var _ experiment = (*setSkipCount)(nil)

type setAllowRepeatReads struct {
	allowRepeatReads bool
}

func (s *setAllowRepeatReads) apply(_ context.Context, o *options) error {
	o.allowRepeatReads = s.allowRepeatReads

	return nil
}

var _ experiment = (*setAllowRepeatReads)(nil)

type skipReads struct {
	skipCount     int
	sleepDuration time.Duration
}

var _ experiment = (*skipReads)(nil)

func (s skipReads) apply(_ context.Context, p *options) error {
	p.readMiddleware = append(p.readMiddleware, s.middleware)

	return nil
}

func (s skipReads) middleware(fn readMethod) readMethod {
	return func(ctx context.Context, info readInfo) (time.Duration, error) {
		for range s.skipCount {
			_, err := fn(ctx, info)
			if err != nil {
				return 0, fmt.Errorf("failed to make uncached gcs read: %w", err)
			}
		}

		if s.sleepDuration > 0 {
			time.Sleep(s.sleepDuration)
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
