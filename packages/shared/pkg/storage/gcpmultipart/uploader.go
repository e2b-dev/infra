package gcpmultipart

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/zap"
	"golang.org/x/oauth2/google"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const ChunkSize = 50 * 1024 * 1024

var httpClient = &retryablehttp.Client{
	RetryMax:     9,
	RetryWaitMin: 10 * time.Millisecond,
	RetryWaitMax: 10 * time.Second,
	CheckRetry:   retryablehttp.DefaultRetryPolicy,
	Logger:       &leveledLogger{logger: logger.L().Detach(context.Background())},
	HTTPClient: &http.Client{
		Transport: otelhttp.NewTransport(&http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			IdleConnTimeout:     90 * time.Second,
			WriteBufferSize:     4 << 20,
			ReadBufferSize:      64 << 10,
			ForceAttemptHTTP2:   true,
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
		}),
	},
	Backoff: func(start, maxBackoff time.Duration, attempt int, _ *http.Response) time.Duration {
		b := start
		for range attempt {
			b = time.Duration(float64(b) * 2)
			if b > maxBackoff {
				b = maxBackoff

				break
			}
		}

		if b > 0 {
			return time.Duration(rand.Int63n(int64(b)))
		}

		return 0
	},
}

type Uploader struct {
	token   string
	baseURL string
	client  *retryablehttp.Client
}

func NewUploader(ctx context.Context, bucketName, objectName string) (*Uploader, error) {
	creds, err := google.FindDefaultCredentials(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, fmt.Errorf("get credentials: %w", err)
	}

	token, err := creds.TokenSource.Token()
	if err != nil {
		return nil, fmt.Errorf("get token: %w", err)
	}

	return &Uploader{
		token:   token.AccessToken,
		baseURL: "https://" + bucketName + ".storage.googleapis.com/" + objectName,
		client:  httpClient,
	}, nil
}

func (u *Uploader) Upload(ctx context.Context, data []byte, maxConcurrency int) (int64, error) {
	uploadID, err := u.initiate(ctx)
	if err != nil {
		return 0, err
	}

	dataLen := len(data)
	numParts := (dataLen + ChunkSize - 1) / ChunkSize
	if numParts == 0 {
		numParts = 1
	}

	parts := make([]xmlPart, numParts)
	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrency)

	for i := range numParts {
		g.Go(func() error {
			start := i * ChunkSize
			end := min(start+ChunkSize, dataLen)
			partNum := i + 1

			etag, err := u.putPart(gCtx, uploadID, partNum, data[start:end])
			if err != nil {
				return err
			}

			parts[i] = xmlPart{PartNumber: partNum, ETag: etag}

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return 0, err
	}

	if err := u.complete(ctx, uploadID, parts); err != nil {
		return 0, err
	}

	return int64(dataLen), nil
}

type xmlInitiateResponse struct {
	UploadID string `xml:"UploadId"`
}

type xmlCompleteRequest struct {
	XMLName string    `xml:"CompleteMultipartUpload"`
	Parts   []xmlPart `xml:"Part"`
}

type xmlPart struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

func (u *Uploader) doRequest(ctx context.Context, method, url string, body io.ReadSeeker, headers [][2]string) (*http.Response, error) {
	req, err := retryablehttp.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+u.token)
	for _, h := range headers {
		req.Header.Set(h[0], h[1])
	}

	resp, err := u.client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)

		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, body)
	}

	return resp, nil
}

func (u *Uploader) initiate(ctx context.Context) (string, error) {
	resp, err := u.doRequest(ctx, "POST", u.baseURL+"?uploads", nil, [][2]string{
		{"Content-Length", "0"},
		{"Content-Type", "application/octet-stream"},
	})
	if err != nil {
		return "", fmt.Errorf("initiate upload: %w", err)
	}
	defer resp.Body.Close()

	var result xmlInitiateResponse
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("parse initiate response: %w", err)
	}

	return result.UploadID, nil
}

func (u *Uploader) putPart(ctx context.Context, uploadID string, partNumber int, data []byte) (string, error) {
	resp, err := u.doRequest(ctx, "PUT",
		u.baseURL+"?partNumber="+strconv.Itoa(partNumber)+"&uploadId="+uploadID,
		bytes.NewReader(data),
		[][2]string{{"Content-Length", strconv.Itoa(len(data))}},
	)
	if err != nil {
		return "", err
	}
	resp.Body.Close()

	if etag := resp.Header.Get("ETag"); etag != "" {
		return etag, nil
	}

	return "", fmt.Errorf("no ETag for part %d", partNumber)
}

func (u *Uploader) complete(ctx context.Context, uploadID string, parts []xmlPart) error {
	body, err := xml.Marshal(xmlCompleteRequest{Parts: parts})
	if err != nil {
		return err
	}

	resp, err := u.doRequest(ctx, "POST", u.baseURL+"?uploadId="+uploadID, bytes.NewReader(body), [][2]string{
		{"Content-Type", "application/xml"},
		{"Content-Length", strconv.Itoa(len(body))},
	})
	if err != nil {
		return err
	}
	resp.Body.Close()

	return nil
}

var _ retryablehttp.LeveledLogger = &leveledLogger{}

type leveledLogger struct{ logger *zap.Logger }

func (l *leveledLogger) Error(msg string, kv ...any) { l.logger.Error(msg, zap.Any("details", kv)) }
func (l *leveledLogger) Info(msg string, kv ...any)  { l.logger.Info(msg, zap.Any("details", kv)) }
func (l *leveledLogger) Debug(string, ...any)        {}
func (l *leveledLogger) Warn(msg string, kv ...any)  { l.logger.Warn(msg, zap.Any("details", kv)) }
