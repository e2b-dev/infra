package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/bloberror"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blockblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/container"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/sas"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const (
	azureOperationTimeout = 5 * time.Second
	azureWriteTimeout     = 30 * time.Second
	azureReadTimeout      = 15 * time.Second

	azureUploadBlockSize   = 10 * 1024 * 1024 // 10 MB
	azureUploadConcurrency = 8                // eight blocks in flight

	// azureSASClockSkew backdates the SAS start time to tolerate clock skew
	// between this host and the storage service.
	azureSASClockSkew = 10 * time.Minute

	// azureMaxUserDelegationTTL is the Azure-enforced maximum lifetime of a
	// user-delegation SAS (7 days); longer requests are clamped.
	azureMaxUserDelegationTTL = 7 * 24 * time.Hour
)

type azureStorage struct {
	client        *azblob.Client
	container     *container.Client
	sharedKey     *azblob.SharedKeyCredential // nil when only AAD credentials are available
	containerName string
}

var _ StorageProvider = (*azureStorage)(nil)

type azureObject struct {
	container     *container.Client
	containerName string
	path          string
}

var (
	_ Seekable       = (*azureObject)(nil)
	_ Blob           = (*azureObject)(nil)
	_ MetadataReader = (*azureObject)(nil)
)

// newAzureStorage builds a Blob Storage backed provider. The "bucket name"
// from the storage config maps to the blob container name. Authentication
// tries AZURE_STORAGE_CONNECTION_STRING first; otherwise it targets
// https://{AZURE_STORAGE_ACCOUNT_NAME}.blob.core.windows.net with the account
// key (AZURE_STORAGE_ACCOUNT_KEY) when present, falling back to
// azidentity.NewDefaultAzureCredential.
func newAzureStorage(_ context.Context, containerName string) (*azureStorage, error) {
	var (
		client    *azblob.Client
		sharedKey *azblob.SharedKeyCredential
	)

	if connectionString := consts.AzureStorageConnectionString; connectionString != "" {
		var err error
		client, err = azblob.NewClientFromConnectionString(connectionString, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create Azure client from connection string: %w", err)
		}

		accountName, accountKey := parseAzureConnectionString(connectionString)
		if accountName != "" && accountKey != "" {
			sharedKey, err = azblob.NewSharedKeyCredential(accountName, accountKey)
			if err != nil {
				return nil, fmt.Errorf("failed to create Azure shared key credential: %w", err)
			}
		}
	} else {
		accountName := consts.AzureStorageAccountName
		if accountName == "" {
			return nil, errors.New("AZURE_STORAGE_ACCOUNT_NAME environment variable is not set")
		}

		serviceURL := fmt.Sprintf("https://%s.blob.core.windows.net/", accountName)

		if accountKey := consts.AzureStorageAccountKey; accountKey != "" {
			var err error
			sharedKey, err = azblob.NewSharedKeyCredential(accountName, accountKey)
			if err != nil {
				return nil, fmt.Errorf("failed to create Azure shared key credential: %w", err)
			}

			client, err = azblob.NewClientWithSharedKeyCredential(serviceURL, sharedKey, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to create Azure client with shared key: %w", err)
			}
		} else {
			credential, err := azidentity.NewDefaultAzureCredential(nil)
			if err != nil {
				return nil, fmt.Errorf("failed to create Azure default credential: %w", err)
			}

			client, err = azblob.NewClient(serviceURL, credential, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to create Azure client: %w", err)
			}
		}
	}

	return &azureStorage{
		client:        client,
		container:     client.ServiceClient().NewContainerClient(containerName),
		sharedKey:     sharedKey,
		containerName: containerName,
	}, nil
}

// parseAzureConnectionString extracts AccountName and AccountKey from an Azure
// storage connection string (semicolon-separated "key=value" pairs).
func parseAzureConnectionString(connectionString string) (accountName, accountKey string) {
	for part := range strings.SplitSeq(connectionString, ";") {
		key, value, found := strings.Cut(part, "=")
		if !found {
			continue
		}

		switch key {
		case "AccountName":
			accountName = value
		case "AccountKey":
			accountKey = value
		}
	}

	return accountName, accountKey
}

func (s *azureStorage) DeleteObjectsWithPrefix(ctx context.Context, prefix string) error {
	// An empty prefix would match, and delete, every blob in the container.
	if prefix == "" {
		return errors.New("refusing to delete objects with an empty prefix")
	}

	// Azure has no bulk-delete call sized to a single request like S3, so the
	// listing pages and per-blob deletes inherit the caller's context instead
	// of one short operation timeout.
	deleted := 0
	pager := s.container.NewListBlobsFlatPager(&container.ListBlobsFlatOptions{Prefix: &prefix})
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("error when listing blobs with prefix: %w", err)
		}

		for _, item := range page.Segment.BlobItems {
			_, err := s.container.NewBlobClient(*item.Name).Delete(ctx, nil)
			if err != nil && !bloberror.HasCode(err, bloberror.BlobNotFound, bloberror.ContainerNotFound) {
				return fmt.Errorf("error when deleting blob %q: %w", *item.Name, err)
			}

			deleted++
		}
	}

	if deleted == 0 {
		logger.L().Warn(ctx, "No objects found to delete with the given prefix", zap.String("prefix", prefix), zap.String("container", s.containerName))
	}

	return nil
}

func (s *azureStorage) GetDetails() string {
	return fmt.Sprintf("[Azure Storage, container set to %s]", s.containerName)
}

// UploadSignedURL returns a SAS PUT URL. Unlike S3/GCS presigned URLs, Azure's
// Put Blob call additionally requires the request header
// "x-ms-blob-type: BlockBlob"; clients uploading to a *.blob.core.windows.net
// URL must send it or the upload fails with MissingRequiredHeader.
func (s *azureStorage) UploadSignedURL(ctx context.Context, path string, ttl time.Duration) (string, error) {
	now := time.Now().UTC()
	permissions := sas.BlobPermissions{Create: true, Write: true}
	values := sas.BlobSignatureValues{
		Protocol:      sas.ProtocolHTTPS,
		StartTime:     now.Add(-azureSASClockSkew),
		ExpiryTime:    now.Add(ttl),
		Permissions:   permissions.String(),
		ContainerName: s.containerName,
		BlobName:      path,
	}

	var query sas.QueryParameters
	if s.sharedKey != nil {
		var err error
		query, err = values.SignWithSharedKey(s.sharedKey)
		if err != nil {
			return "", fmt.Errorf("failed to sign SAS with account key: %w", err)
		}
	} else {
		// User-delegation SAS lifetimes are capped by Azure at 7 days.
		values.ExpiryTime = now.Add(clampUserDelegationTTL(ttl))

		ctx, cancel := context.WithTimeout(ctx, azureOperationTimeout)
		defer cancel()

		delegationCredential, err := s.client.ServiceClient().GetUserDelegationCredential(
			ctx,
			service.KeyInfo{
				Start:  new(values.StartTime.UTC().Format(sas.TimeFormat)),
				Expiry: new(values.ExpiryTime.UTC().Format(sas.TimeFormat)),
			},
			nil,
		)
		if err != nil {
			return "", fmt.Errorf("failed to get Azure user delegation credential: %w", err)
		}

		query, err = values.SignWithUserDelegation(delegationCredential)
		if err != nil {
			return "", fmt.Errorf("failed to sign SAS with user delegation credential: %w", err)
		}
	}

	return fmt.Sprintf("%s?%s", s.container.NewBlobClient(path).URL(), query.Encode()), nil
}

// clampUserDelegationTTL bounds a requested SAS lifetime to the Azure
// user-delegation maximum of 7 days.
func clampUserDelegationTTL(ttl time.Duration) time.Duration {
	if ttl > azureMaxUserDelegationTTL {
		return azureMaxUserDelegationTTL
	}

	return ttl
}

func (s *azureStorage) OpenSeekable(_ context.Context, path string) (Seekable, error) {
	return &azureObject{
		container:     s.container,
		containerName: s.containerName,
		path:          path,
	}, nil
}

func (s *azureStorage) OpenBlob(_ context.Context, path string) (Blob, error) {
	return &azureObject{
		container:     s.container,
		containerName: s.containerName,
		path:          path,
	}, nil
}

func (o *azureObject) WriteTo(ctx context.Context, dst io.Writer) (n int64, err error) {
	start := time.Now()
	defer func() { RecordReadBlob(ctx, time.Since(start), n, o.path, SourceAzure, err) }()

	ctx, cancel := context.WithTimeout(ctx, azureReadTimeout)
	defer cancel()

	resp, err := o.container.NewBlobClient(o.path).DownloadStream(ctx, nil)
	if err != nil {
		if bloberror.HasCode(err, bloberror.BlobNotFound, bloberror.ContainerNotFound) {
			return 0, ErrObjectNotExist
		}

		return 0, err
	}

	defer resp.Body.Close()

	n, err = io.Copy(dst, resp.Body)

	return n, err
}

func (o *azureObject) StoreFile(ctx context.Context, path string, opts ...PutOption) (*FullFrameTable, [32]byte, error) {
	p := ApplyPutOptions(opts)
	if CompressConfigFromOpts(p).IsCompressionEnabled() {
		return nil, [32]byte{}, errors.New("compressed uploads are not supported on Azure (builds target GCP only)")
	}

	// Inherit the caller's context for the block upload. UploadFile stages
	// blocks concurrently (Concurrency=8, BlockSize=10MB) and commits the block
	// list at the end — a tight static timeout here would cancel an in-flight
	// multi-GB snapshot upload. The caller (pkg/server/sandboxes.go) already
	// scopes a per-attempt deadline (uploadTimeout = 20m) with retry budget on
	// top, matching the AWS/GCP paths which also inherit the caller's ctx.
	f, err := os.Open(path)
	if err != nil {
		return nil, [32]byte{}, fmt.Errorf("failed to open file %s: %w", path, err)
	}
	defer f.Close()

	_, err = o.container.NewBlockBlobClient(o.path).UploadFile(
		ctx,
		f,
		&blockblob.UploadFileOptions{
			BlockSize:   azureUploadBlockSize,
			Concurrency: azureUploadConcurrency,
			Metadata:    encodeAzureMetadata(p.Metadata),
		},
	)
	if err == nil {
		fi, _ := f.Stat()
		var size int64
		if fi != nil {
			size = fi.Size()
		}

		logger.L().Debug(ctx, "Uploaded file to Azure Blob Storage",
			zap.String("container", o.containerName),
			zap.String("object", o.path),
			zap.String("source", path),
			zap.Int64("size_uncompressed", size),
			zap.String("compression", "none"),
		)
	}

	return nil, [32]byte{}, err
}

func (o *azureObject) Put(ctx context.Context, data []byte, opts ...PutOption) error {
	ctx, cancel := context.WithTimeout(ctx, azureWriteTimeout)
	defer cancel()

	_, err := o.container.NewBlockBlobClient(o.path).UploadBuffer(
		ctx,
		data,
		&blockblob.UploadBufferOptions{
			Metadata: encodeAzureMetadata(ApplyPutOptions(opts).Metadata),
		},
	)
	if err != nil {
		return err
	}

	return nil
}

func (o *azureObject) OpenRangeReader(ctx context.Context, off, length int64, frameTable *FrameTable) (_ RangeReader, _ Source, err error) {
	start := time.Now()
	objType, _ := seekableObjectType(o.path)
	defer func() {
		RecordReadOpen(ctx, time.Since(start), objType, SourceAzure, frameTable.CompressionType(), err)
	}()

	if frameTable.IsCompressed() {
		return nil, SourceAzure, errors.New("compressed reads are not supported on Azure")
	}

	// In azblob a Count of 0 means CountToEnd, which would silently invert a
	// zero-length request into a read of the whole blob tail.
	if length <= 0 {
		return nil, SourceAzure, fmt.Errorf("invalid range length %d for %q", length, o.path)
	}

	resp, err := o.container.NewBlobClient(o.path).DownloadStream(
		ctx,
		&blob.DownloadStreamOptions{
			Range: blob.HTTPRange{Offset: off, Count: length},
		},
	)
	if err != nil {
		if bloberror.HasCode(err, bloberror.BlobNotFound, bloberror.ContainerNotFound) {
			return nil, SourceAzure, ErrObjectNotExist
		}

		return nil, SourceAzure, fmt.Errorf("failed to create Azure range reader for %q: %w", o.path, err)
	}

	return NewRangeReader(resp.Body), SourceAzure, nil
}

func (o *azureObject) Size(ctx context.Context) (_ int64, err error) {
	start := time.Now()
	objType, _ := seekableObjectType(o.path)
	defer func() { RecordReadSize(ctx, time.Since(start), objType, SourceAzure, err) }()

	ctx, cancel := context.WithTimeout(ctx, azureOperationTimeout)
	defer cancel()

	resp, err := o.container.NewBlobClient(o.path).GetProperties(ctx, nil)
	if err != nil {
		if bloberror.HasCode(err, bloberror.BlobNotFound, bloberror.ContainerNotFound) {
			return 0, ErrObjectNotExist
		}

		return 0, err
	}

	return *resp.ContentLength, nil
}

// Metadata implements MetadataReader via GetProperties (always hits Azure).
func (o *azureObject) Metadata(ctx context.Context) (ObjectMetadata, error) {
	ctx, cancel := context.WithTimeout(ctx, azureOperationTimeout)
	defer cancel()

	resp, err := o.container.NewBlobClient(o.path).GetProperties(ctx, nil)
	if err != nil {
		if bloberror.HasCode(err, bloberror.BlobNotFound, bloberror.ContainerNotFound) {
			return nil, fmt.Errorf("failed to get Azure blob (%q) properties: %w", o.path, ErrObjectNotExist)
		}

		return nil, fmt.Errorf("failed to get Azure blob (%q) properties: %w", o.path, err)
	}

	return decodeAzureMetadata(resp.Metadata), nil
}

func (o *azureObject) Exists(ctx context.Context) (bool, error) {
	_, err := o.Size(ctx)

	return err == nil, ignoreNotExists(err)
}

func (o *azureObject) Delete(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, azureOperationTimeout)
	defer cancel()

	// Deleting a missing blob succeeds, matching S3 DeleteObject semantics.
	_, err := o.container.NewBlobClient(o.path).Delete(ctx, nil)
	if err != nil && !bloberror.HasCode(err, bloberror.BlobNotFound, bloberror.ContainerNotFound) {
		return err
	}

	return nil
}

// encodeAzureMetadata converts object metadata to the map[string]*string shape
// the Azure SDK expects. Azure metadata keys must be valid C# identifiers, so
// hyphens (used by several storage-index keys) are encoded as double
// underscores; no existing key contains one.
func encodeAzureMetadata(metadata ObjectMetadata) map[string]*string {
	if len(metadata) == 0 {
		return nil
	}

	encoded := make(map[string]*string, len(metadata))
	for k, v := range metadata {
		// A literal "__" in a key would decode back as "-", corrupting it.
		if strings.Contains(k, "__") {
			logger.L().Error(context.Background(), "metadata key contains '__' and cannot be stored on Azure without corrupting on decode; skipping", zap.String("key", k))

			continue
		}

		encoded[strings.ReplaceAll(k, "-", "__")] = new(v)
	}

	return encoded
}

// decodeAzureMetadata reverses encodeAzureMetadata. Azure returns metadata
// keys case-normalized, so they are lowercased before decoding.
func decodeAzureMetadata(metadata map[string]*string) ObjectMetadata {
	if len(metadata) == 0 {
		return nil
	}

	decoded := make(ObjectMetadata, len(metadata))
	for k, v := range metadata {
		var value string
		if v != nil {
			value = *v
		}

		decoded[strings.ReplaceAll(strings.ToLower(k), "__", "-")] = value
	}

	return decoded
}
