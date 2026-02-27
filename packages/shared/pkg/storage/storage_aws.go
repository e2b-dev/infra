package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	awsOperationTimeout = 5 * time.Second
	awsWriteTimeout     = 30 * time.Second
	awsReadTimeout      = 15 * time.Second
)

type awsStorage struct {
	client        *s3.Client
	presignClient *s3.PresignClient
	bucketName    string
}

var _ StorageProvider = (*awsStorage)(nil)

type awsObject struct {
	client     *s3.Client
	path       string
	bucketName string
}

var (
	_ FramedFile = (*awsObject)(nil)
	_ Blob       = (*awsObject)(nil)
)

func newAWSStorage(ctx context.Context, bucketName string) (*awsStorage, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}

	client := s3.NewFromConfig(cfg)
	presignClient := s3.NewPresignClient(client)

	return &awsStorage{
		client:        client,
		presignClient: presignClient,
		bucketName:    bucketName,
	}, nil
}

func (s *awsStorage) DeleteObjectsWithPrefix(ctx context.Context, prefix string) error {
	ctx, cancel := context.WithTimeout(ctx, awsOperationTimeout)
	defer cancel()

	list, err := s.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: &s.bucketName, Prefix: &prefix})
	if err != nil {
		return err
	}

	objects := make([]types.ObjectIdentifier, 0, len(list.Contents))
	for _, obj := range list.Contents {
		objects = append(objects, types.ObjectIdentifier{Key: obj.Key})
	}

	// AWS S3 delete operation requires at least one object to delete.
	if len(objects) == 0 {
		logger.L().Warn(ctx, "No objects found to delete with the given prefix", zap.String("prefix", prefix), zap.String("bucket", s.bucketName))

		return nil
	}

	output, err := s.client.DeleteObjects(
		ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(s.bucketName),
			Delete: &types.Delete{Objects: objects},
		},
	)
	if err != nil {
		return err
	}

	if len(output.Errors) > 0 {
		var errStr strings.Builder
		for _, delErr := range output.Errors {
			errStr.WriteString(fmt.Sprintf("Key: %s, Code: %s, Message: %s; ", aws.ToString(delErr.Key), aws.ToString(delErr.Code), aws.ToString(delErr.Message)))
		}

		return errors.New("errors occurred during deletion: " + errStr.String())
	}

	if len(output.Deleted) != len(objects) {
		return errors.New("not all objects listed were deleted")
	}

	return nil
}

func (s *awsStorage) GetDetails() string {
	return fmt.Sprintf("[AWS Storage, bucket set to %s]", s.bucketName)
}

func (s *awsStorage) UploadSignedURL(ctx context.Context, path string, ttl time.Duration) (string, error) {
	input := &s3.PutObjectInput{
		Bucket: aws.String(s.bucketName),
		Key:    aws.String(path),
	}
	resp, err := s.presignClient.PresignPutObject(ctx, input, func(opts *s3.PresignOptions) {
		opts.Expires = ttl
	})
	if err != nil {
		return "", fmt.Errorf("failed to presign PUT URL: %w", err)
	}

	return resp.URL, nil
}

func (s *awsStorage) OpenFramedFile(_ context.Context, path string) (FramedFile, error) {
	return &awsObject{
		client:     s.client,
		bucketName: s.bucketName,
		path:       path,
	}, nil
}

func (s *awsStorage) OpenBlob(_ context.Context, path string) (Blob, error) {
	return &awsObject{
		client:     s.client,
		bucketName: s.bucketName,
		path:       path,
	}, nil
}

func (o *awsObject) WriteTo(ctx context.Context, dst io.Writer) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, awsReadTimeout)
	defer cancel()

	resp, err := o.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &o.bucketName, Key: &o.path})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return 0, ErrObjectNotExist
		}

		return 0, err
	}

	defer resp.Body.Close()

	return io.Copy(dst, resp.Body)
}

func (o *awsObject) StoreFile(ctx context.Context, path string, opts *FramedUploadOptions) (*FrameTable, error) {
	if opts != nil && opts.CompressionType != CompressionNone {
		return nil, fmt.Errorf("compressed uploads are not supported on AWS (builds target GCP only)")
	}

	ctx, cancel := context.WithTimeout(ctx, awsWriteTimeout)
	defer cancel()

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file %s: %w", path, err)
	}
	defer f.Close()

	uploader := manager.NewUploader(
		o.client,
		func(u *manager.Uploader) {
			u.PartSize = 10 * 1024 * 1024 // 10 MB
			u.Concurrency = 8             // eight parts in flight
		},
	)

	_, err = uploader.Upload(
		ctx,
		&s3.PutObjectInput{
			Bucket: &o.bucketName,
			Key:    &o.path,
			Body:   f,
		},
	)

	return nil, err
}

func (o *awsObject) Put(ctx context.Context, data []byte) error {
	ctx, cancel := context.WithTimeout(ctx, awsWriteTimeout)
	defer cancel()

	_, err := o.client.PutObject(
		ctx,
		&s3.PutObjectInput{
			Bucket: &o.bucketName,
			Key:    &o.path,
			Body:   bytes.NewReader(data),
		},
	)
	if err != nil {
		return err
	}

	return nil
}

func (o *awsObject) openRangeReader(ctx context.Context, off int64, length int) (io.ReadCloser, error) {
	readRange := aws.String(fmt.Sprintf("bytes=%d-%d", off, off+int64(length)-1))
	resp, err := o.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(o.bucketName),
		Key:    aws.String(o.path),
		Range:  readRange,
	})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, ErrObjectNotExist
		}

		return nil, fmt.Errorf("failed to create S3 range reader for %q: %w", o.path, err)
	}

	return resp.Body, nil
}

func (o *awsObject) Size(ctx context.Context) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, awsOperationTimeout)
	defer cancel()

	resp, err := o.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &o.bucketName, Key: &o.path})
	if err != nil {
		var nsk *types.NoSuchKey
		var nfd *types.NotFound
		if errors.As(err, &nsk) || errors.As(err, &nfd) {
			return 0, ErrObjectNotExist
		}

		return 0, err
	}

	if v, ok := resp.Metadata["uncompressed-size"]; ok {
		parsed, parseErr := strconv.ParseInt(v, 10, 64)
		if parseErr == nil {
			return parsed, nil
		}
	}

	return *resp.ContentLength, nil
}

func (o *awsObject) Exists(ctx context.Context) (bool, error) {
	_, err := o.Size(ctx)

	return err == nil, ignoreNotExists(err)
}

func (o *awsObject) Delete(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, awsOperationTimeout)
	defer cancel()

	_, err := o.client.DeleteObject(
		ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(o.bucketName),
			Key:    aws.String(o.path),
		},
	)

	return err
}

func ignoreNotExists(err error) error {
	if errors.Is(err, ErrObjectNotExist) {
		return nil
	}

	return err
}

func (o *awsObject) GetFrame(ctx context.Context, offsetU int64, frameTable *FrameTable, decompress bool, buf []byte, readSize int64, onRead func(totalWritten int64)) (Range, error) {
	return getFrame(ctx, o.openRangeReader, "S3:"+o.path, offsetU, frameTable, decompress, buf, readSize, onRead)
}
