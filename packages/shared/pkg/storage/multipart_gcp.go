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

type multipartUploaderGCP struct {
	bucketName  string
	objectName  string
	token       string
	client      *retryablehttp.Client
	retryConfig RetryConfig
	baseURL     string // Allow overriding for testing
	etags       *sync.Map
}

var _ MultipartUploader = (*multipartUploaderGCP)(nil)

func NewGCPUploaderWithRetryConfig(ctx context.Context, bucketName, objectName string, retryConfig RetryConfig) (*multipartUploaderGCP, error) {
	creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, fmt.Errorf("failed to get credentials: %w", err)
	}

	token, err := creds.TokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("failed to get token: %w", err)
	}

	return &multipartUploaderGCP{
		etags:       &sync.Map{},
		bucketName:  bucketName,
		objectName:  objectName,
		token:       token.AccessToken,
		client:      createRetryableClient(ctx, retryConfig),
		retryConfig: retryConfig,
		baseURL:     fmt.Sprintf("https://%s.storage.googleapis.com", bucketName),
	}, nil
}

func (m *multipartUploaderGCP) InitiateUpload() (string, error) {
	url := fmt.Sprintf("%s/%s?uploads", m.baseURL, m.objectName)

	req, err := retryablehttp.NewRequest("POST", url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+m.token)
	req.Header.Set("Content-Length", "0")
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := m.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)

		return "", fmt.Errorf("failed to initiate upload (status %d): %s", resp.StatusCode, string(body))
	}

	var result InitiateMultipartUploadResult
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to parse initiate response: %w", err)
	}

	return result.UploadID, nil
}

func (m *multipartUploaderGCP) UploadPart(uploadID string, partNumber int, dataList ...[]byte) error {
	// Calculate MD5 for data integrity
	hasher := md5.New()
	l := 0
	for _, data := range dataList {
		_, _ = hasher.Write(data)
		l += len(data)
	}
	md5Sum := base64.StdEncoding.EncodeToString(hasher.Sum(nil))

	url := fmt.Sprintf("%s/%s?partNumber=%d&uploadId=%s",
		m.baseURL, m.objectName, partNumber, uploadID)

	req, err := retryablehttp.NewRequest("PUT", url, newVectorReader(dataList))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+m.token)
	req.Header.Set("Content-Length", fmt.Sprintf("%d", l))
	req.Header.Set("Content-MD5", md5Sum)

	resp, err := m.client.Do(req)
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
	m.etags.Store(partNumber, etag)

	return nil
}

func (m *multipartUploaderGCP) CompleteUpload(uploadID string) error {
	// Collect parts
	parts := make([]Part, 0)
	m.etags.Range(func(key, value any) bool {
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
		m.baseURL, m.objectName, uploadID)

	req, err := retryablehttp.NewRequest("POST", url, bytes.NewReader(xmlData))
	if err != nil {
		return err
	}

	req.Header.Set("Authorization", "Bearer "+m.token)
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(xmlData)))

	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)

		return fmt.Errorf("failed to complete upload (status %d): %s", resp.StatusCode, string(body))
	}

	return nil
}
