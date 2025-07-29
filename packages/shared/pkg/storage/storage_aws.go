package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

const (
	awsOperationTimeout = 5 * time.Second
	awsWriteTimeout     = 30 * time.Second
	awsReadTimeout      = 15 * time.Second
)

type AWSBucketStorageProvider struct {
	client        *s3.Client
	presignClient *s3.PresignClient
	bucketName    string
}

type AWSBucketStorageObjectProvider struct {
	client     *s3.Client
	path       string
	bucketName string
	ctx        context.Context
}

func NewAWSBucketStorageProvider(ctx context.Context, bucketName string) (*AWSBucketStorageProvider, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}

	client := s3.NewFromConfig(cfg)
	presignClient := s3.NewPresignClient(client)

	return &AWSBucketStorageProvider{
		client:        client,
		presignClient: presignClient,
		bucketName:    bucketName,
	}, nil
}

func (a *AWSBucketStorageProvider) DeleteObjectsWithPrefix(ctx context.Context, prefix string) error {
	ctx, cancel := context.WithTimeout(ctx, awsOperationTimeout)
	defer cancel()

	list, err := a.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: &a.bucketName, Prefix: &prefix})
	if err != nil {
		return err
	}

	objects := make([]types.ObjectIdentifier, 0, len(list.Contents))
	for _, obj := range list.Contents {
		objects = append(objects, types.ObjectIdentifier{Key: obj.Key})
	}

	_, err = a.client.DeleteObjects(
		ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(a.bucketName),
			Delete: &types.Delete{Objects: objects},
		},
	)

	return err
}

func (a *AWSBucketStorageProvider) GetDetails() string {
	return fmt.Sprintf("[AWS Storage, bucket set to %s]", a.bucketName)
}

func (a *AWSBucketStorageProvider) UploadSignedURL(ctx context.Context, path string, ttl time.Duration) (string, error) {
	input := &s3.PutObjectInput{
		Bucket: aws.String(a.bucketName),
		Key:    aws.String(path),
	}
	resp, err := a.presignClient.PresignPutObject(ctx, input, func(opts *s3.PresignOptions) {
		opts.Expires = ttl
	})
	if err != nil {
		return "", fmt.Errorf("failed to presign PUT URL: %w", err)
	}
	return resp.URL, nil
}

func (a *AWSBucketStorageProvider) OpenObject(ctx context.Context, path string) (StorageObjectProvider, error) {
	return &AWSBucketStorageObjectProvider{
		client:     a.client,
		bucketName: a.bucketName,
		path:       path,
		ctx:        ctx,
	}, nil
}

func (a *AWSBucketStorageObjectProvider) WriteTo(dst io.Writer) (int64, error) {
	ctx, cancel := context.WithTimeout(a.ctx, awsReadTimeout)
	defer cancel()

	resp, err := a.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &a.bucketName, Key: &a.path})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return 0, ErrorObjectNotExist
		}

		return 0, err
	}

	defer resp.Body.Close()

	return io.Copy(dst, resp.Body)
}

func (a *AWSBucketStorageObjectProvider) WriteFrom(file io.ReadCloser, length int64) error {
	ctx, cancel := context.WithTimeout(a.ctx, awsWriteTimeout)
	defer cancel()

	uploader := manager.NewUploader(
		a.client,
		func(u *manager.Uploader) {
			u.PartSize = 10 * 1024 * 1024 // 10 MB
			u.Concurrency = 8             // eight parts in flight
		},
	)

	_, err := uploader.Upload(
		ctx,
		&s3.PutObjectInput{
			Bucket: &a.bucketName,
			Key:    &a.path,
			Body:   file,
		},
	)

	return err
}

func (a *AWSBucketStorageObjectProvider) ReadFrom(src io.Reader) (int64, error) {
	ctx, cancel := context.WithTimeout(a.ctx, awsWriteTimeout)
	defer cancel()

	_, err := a.client.PutObject(
		ctx,
		&s3.PutObjectInput{
			Bucket: &a.bucketName,
			Key:    &a.path,
			Body:   src,
		},
	)
	if err != nil {
		return 0, err
	}

	return 0, nil
}

func (a *AWSBucketStorageObjectProvider) ReadAt(buff []byte, off int64) (n int, err error) {
	ctx, cancel := context.WithTimeout(a.ctx, awsReadTimeout)
	defer cancel()

	readRange := aws.String(fmt.Sprintf("bytes=%d-%d", off, off+int64(len(buff))-1))
	resp, err := a.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(a.bucketName),
		Key:    aws.String(a.path),
		Range:  readRange,
	})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return 0, ErrorObjectNotExist
		}

		return 0, err
	}

	defer resp.Body.Close()

	// When the object is smaller than requested range there will be unexpected EOF,
	// but backend expects to return EOF in this case.
	n, err = io.ReadFull(resp.Body, buff)
	if errors.Is(err, io.ErrUnexpectedEOF) {
		err = io.EOF
	}

	return n, err
}

func (a *AWSBucketStorageObjectProvider) Size() (int64, error) {
	ctx, cancel := context.WithTimeout(a.ctx, awsOperationTimeout)
	defer cancel()

	resp, err := a.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &a.bucketName, Key: &a.path})
	if err != nil {
		return 0, err
	}

	return *resp.ContentLength, nil
}

func (a *AWSBucketStorageObjectProvider) Delete() error {
	ctx, cancel := context.WithTimeout(a.ctx, awsOperationTimeout)
	defer cancel()

	_, err := a.client.DeleteObject(
		ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(a.bucketName),
			Key:    aws.String(a.path),
		},
	)

	return err
}
