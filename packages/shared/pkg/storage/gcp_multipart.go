package storage

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"

	"github.com/hashicorp/go-retryablehttp"
	"golang.org/x/oauth2/google"
)

type InitiateMultipartUploadResult struct {
	Bucket   string `xml:"Bucket"`
	Key      string `xml:"Key"`
	UploadID string `xml:"UploadId"`
}

type CompleteMultipartUpload struct {
	XMLName string `xml:"CompleteMultipartUpload"`
	Parts   []Part `xml:"Part"`
}

type Part struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

type gcpMultipartUploader struct {
	g           *GCP
	objectName  string
	token       string
	client      *retryablehttp.Client
	retryConfig RetryConfig
	etags       *sync.Map
	uploadID    string
	metadata    map[string]string
}

func (g *GCP) MakeMultipartUpload(ctx context.Context, objectName string, retryConfig RetryConfig, metadata map[string]string) (MultipartUploader, func(), int, error) {
	cleanup := func() {}
	creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, cleanup, 0, fmt.Errorf("failed to get credentials: %w", err)
	}

	token, err := creds.TokenSource.Token()
	if err != nil {
		return nil, cleanup, 0, fmt.Errorf("failed to get token: %w", err)
	}

	if g.limiter != nil {
		uploadLimiter := g.limiter.GCloudUploadLimiter()
		if uploadLimiter != nil {
			semaphoreErr := uploadLimiter.Acquire(ctx, 1)
			if semaphoreErr != nil {
				return nil, cleanup, 0, fmt.Errorf("failed to acquire semaphore: %w", semaphoreErr)
			}
			cleanup = func() { uploadLimiter.Release(1) }
		}
	}

	uploadConcurrency := g.limiter.GCloudMaxTasks(ctx)

	return &gcpMultipartUploader{
		g:           g,
		etags:       &sync.Map{},
		objectName:  objectName,
		token:       token.AccessToken,
		client:      createRetryableClient(ctx, retryConfig),
		retryConfig: retryConfig,
		metadata:    metadata,
	}, cleanup, uploadConcurrency, nil
}

func (u *gcpMultipartUploader) Start(ctx context.Context) error {
	url := fmt.Sprintf("%s/%s?uploads", u.g.baseUploadURL, u.objectName)

	req, err := retryablehttp.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+u.token)
	req.Header.Set("Content-Length", "0")
	req.Header.Set("Content-Type", "application/octet-stream")

	// Set custom metadata headers
	for k, v := range u.metadata {
		req.Header.Set("x-goog-meta-"+k, v)
	}

	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)

		return fmt.Errorf("failed to initiate upload (status %d): %s", resp.StatusCode, string(body))
	}
	var result InitiateMultipartUploadResult
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("failed to parse initiate response: %w", err)
	}
	u.uploadID = result.UploadID

	return nil
}

func (u *gcpMultipartUploader) UploadPart(ctx context.Context, partNumber int, dataList ...[]byte) error {
	// Calculate MD5 for data integrity
	hasher := md5.New()
	l := 0
	for _, data := range dataList {
		_, _ = hasher.Write(data)
		l += len(data)
	}
	md5Sum := base64.StdEncoding.EncodeToString(hasher.Sum(nil))

	url := fmt.Sprintf("%s/%s?partNumber=%d&uploadId=%s",
		u.g.baseUploadURL, u.objectName, partNumber, u.uploadID)

	var r io.Reader
	if len(dataList) == 1 {
		r = bytes.NewReader(dataList[0])
	} else {
		r = newMultiReader(dataList)
	}
	req, err := retryablehttp.NewRequestWithContext(ctx, "PUT", url, r)
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+u.token)
	req.Header.Set("Content-Length", fmt.Sprintf("%d", l))
	req.Header.Set("Content-MD5", md5Sum)

	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)

		return fmt.Errorf("failed to upload part %d (status %d): %s", partNumber, resp.StatusCode, string(body))
	}

	etag := resp.Header.Get("ETag")
	if etag == "" {
		return fmt.Errorf("no ETag returned for part %d", partNumber)
	}
	u.etags.Store(partNumber, etag)

	return nil
}

func (u *gcpMultipartUploader) Complete(ctx context.Context) error {
	// Collect parts
	parts := make([]Part, 0)
	u.etags.Range(func(key, value any) bool {
		partNumber, ok := key.(int)
		if !ok {
			return false
		}
		etag, ok := value.(string)
		if !ok {
			return false
		}
		parts = append(parts, Part{
			PartNumber: partNumber,
			ETag:       etag,
		})

		return true
	})

	// Sort parts by part number
	sort.Slice(parts, func(i, j int) bool {
		return parts[i].PartNumber < parts[j].PartNumber
	})

	completeReq := CompleteMultipartUpload{Parts: parts}
	xmlData, err := xml.Marshal(completeReq)
	if err != nil {
		return fmt.Errorf("failed to marshal complete request: %w", err)
	}

	url := fmt.Sprintf("%s/%s?uploadId=%s",
		u.g.baseUploadURL, u.objectName, u.uploadID)

	req, err := retryablehttp.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(xmlData))
	if err != nil {
		return fmt.Errorf("failed to create complete request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+u.token)
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(xmlData)))

	resp, err := u.client.Do(req)
	if err != nil {
		return fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)

		return fmt.Errorf("failed to complete upload (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}
