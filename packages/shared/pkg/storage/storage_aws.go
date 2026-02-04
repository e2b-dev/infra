package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
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

type AWS struct {
	client        *s3.Client
	presignClient *s3.PresignClient
	bucketName    string
}

var (
	_ Basic          = (*AWS)(nil)
	_ PublicUploader = (*AWS)(nil)
)

func newAWSBackend(ctx context.Context, bucketName string) (*Backend, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}

	client := s3.NewFromConfig(cfg)
	presignClient := s3.NewPresignClient(client)

	aws := &AWS{
		client:        client,
		presignClient: presignClient,
		bucketName:    bucketName,
	}

	return &Backend{
		Basic:          aws,
		PublicUploader: aws,
		Manager:        aws,
	}, nil
}

func (p *AWS) String() string {
	return fmt.Sprintf("[AWS Storage, bucket set to %s]", p.bucketName)
}

func (p *AWS) DeleteWithPrefix(ctx context.Context, prefix string) error {
	ctx, cancel := context.WithTimeout(ctx, awsOperationTimeout)
	defer cancel()

	list, err := p.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: &p.bucketName, Prefix: &prefix})
	if err != nil {
		return err
	}

	objects := make([]types.ObjectIdentifier, 0, len(list.Contents))
	for _, obj := range list.Contents {
		objects = append(objects, types.ObjectIdentifier{Key: obj.Key})
	}

	// AWS S3 delete operation requires at least one object to delete.
	if len(objects) == 0 {
		logger.L().Warn(ctx, "No objects found to delete with the given prefix", zap.String("prefix", prefix), zap.String("bucket", p.bucketName))

		return nil
	}

	output, err := p.client.DeleteObjects(
		ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(p.bucketName),
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

func (p *AWS) PublicUploadURL(ctx context.Context, path string, ttl time.Duration) (string, error) {
	input := &s3.PutObjectInput{
		Bucket: aws.String(p.bucketName),
		Key:    aws.String(path),
	}
	resp, err := p.presignClient.PresignPutObject(ctx, input, func(opts *s3.PresignOptions) {
		opts.Expires = ttl
	})
	if err != nil {
		return "", fmt.Errorf("failed to presign PUT URL: %w", err)
	}

	return resp.URL, nil
}

func (p *AWS) StartDownload(ctx context.Context, path string) (io.ReadCloser, error) {
	resp, err := p.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &p.bucketName, Key: &path})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, ErrObjectNotExist
		}

		return nil, err
	}

	return resp.Body, nil
}

// func (s *AWS) StoreFile(ctx context.Context, path string) error {
// 	ctx, cancel := context.WithTimeout(ctx, awsWriteTimeout)
// 	defer cancel()

// 	f, err := os.Open(path)
// 	if err != nil {
// 		return fmt.Errorf("failed to open file %s: %w", path, err)
// 	}
// 	defer f.Close()

// 	uploader := manager.NewUploader(
// 		s.client,
// 		func(u *manager.Uploader) {
// 			u.PartSize = 10 * 1024 * 1024 // 10 MB
// 			u.Concurrency = 8             // eight parts in flight
// 		},
// 	)

// 	_, err = uploader.Upload(
// 		ctx,
// 		&s3.PutObjectInput{
// 			Bucket: &s.bucketName,
// 			Key:    &path,
// 			Body:   f,
// 		},
// 	)

// 	return err
// }

func (p *AWS) Upload(ctx context.Context, path string, in io.Reader) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, awsWriteTimeout)
	defer cancel()

	_, err := p.client.PutObject(
		ctx,
		&s3.PutObjectInput{
			Bucket: &p.bucketName,
			Key:    &path,
			Body:   in,
		},
	)
	if err != nil {
		return 0, err
	}

	return 0, nil
}

func (p *AWS) RangeGet(ctx context.Context, objectPath string, offset int64, length int) (io.ReadCloser, error) {
	ctx, cancel := context.WithTimeout(ctx, awsReadTimeout)

	readRange := aws.String(fmt.Sprintf("bytes=%d-%d", offset, offset+int64(length-1)))
	resp, err := p.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(p.bucketName),
		Key:    aws.String(objectPath),
		Range:  readRange,
	})
	if err != nil {
		cancel()
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, ErrObjectNotExist
		}

		return nil, err
	}

	return withCancelCloser{ReadCloser: resp.Body, cancelFunc: cancel}, nil
}

func (p *AWS) RawSize(ctx context.Context, path string) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, awsOperationTimeout)
	defer cancel()

	resp, err := p.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &p.bucketName, Key: &path})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return 0, ErrObjectNotExist
		}

		return 0, err
	}

	return *resp.ContentLength, nil
}

func ignoreNotExists(err error) error {
	if errors.Is(err, ErrObjectNotExist) {
		return nil
	}

	return err
}

func (p *AWS) Size(ctx context.Context, path string) (virtSize, rawSize int64, err error) {
	ctx, cancel := context.WithTimeout(ctx, awsOperationTimeout)
	defer cancel()

	resp, err := p.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(p.bucketName),
		Key:    aws.String(path),
	})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return 0, 0, ErrObjectNotExist
		}

		return 0, 0, fmt.Errorf("failed to get S3 object (%q) metadata: %w", path, err)
	}

	rawSize = *resp.ContentLength

	// Check for uncompressed size in metadata (set during compressed upload).
	if resp.Metadata != nil {
		if uncompressedStr, ok := resp.Metadata[MetadataKeyUncompressedSize]; ok {
			if _, err := fmt.Sscanf(uncompressedStr, "%d", &virtSize); err == nil {
				return virtSize, rawSize, nil
			}
		}
	}

	// No metadata means uncompressed file - virt size == raw size.
	return rawSize, rawSize, nil
}
