package storage

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestAWSPartUploader_PartUploaderContract(t *testing.T) {
	t.Parallel()

	testPartUploaderContract(t, []partUploaderTestAdapter{
		{
			name:          "aws",
			abortsOnClose: true,
			new: func(t *testing.T, recorder *partUploaderRecorder) partUploader {
				t.Helper()

				client := newTestS3Client(t, func(w http.ResponseWriter, r *http.Request) {
					switch {
					case r.Method == http.MethodPost && r.URL.RawQuery == "uploads=":
						recorder.started = true
						w.WriteHeader(http.StatusOK)
						w.Write([]byte(`<InitiateMultipartUploadResult><UploadId>contract-upload-id</UploadId></InitiateMultipartUploadResult>`))
					case r.Method == http.MethodPut && strings.Contains(r.URL.RawQuery, "partNumber=1"):
						recorder.parts = append(recorder.parts, recordedPart{number: 1, body: readAllString(t, r.Body)})
						w.Header().Set("ETag", `"etag1"`)
						w.WriteHeader(http.StatusOK)
					case r.Method == http.MethodPut && strings.Contains(r.URL.RawQuery, "partNumber=2"):
						recorder.parts = append(recorder.parts, recordedPart{number: 2, body: readAllString(t, r.Body)})
						w.Header().Set("ETag", `"etag2"`)
						w.WriteHeader(http.StatusOK)
					case r.Method == http.MethodPost && strings.Contains(r.URL.RawQuery, "uploadId=contract-upload-id"):
						var complete completeMultipartUploadRequest
						if err := xml.NewDecoder(r.Body).Decode(&complete); err != nil {
							t.Fatalf("decode complete upload request: %v", err)
						}
						recorder.completed = true
						if len(complete.Parts) == 2 && (complete.Parts[0].PartNumber != 1 || complete.Parts[1].PartNumber != 2) {
							t.Fatalf("complete upload parts not sorted: %+v", complete.Parts)
						}
						w.WriteHeader(http.StatusOK)
						w.Write([]byte(`<CompleteMultipartUploadResult><Bucket>test-bucket</Bucket><Key>test-object</Key><ETag>"complete-etag"</ETag></CompleteMultipartUploadResult>`))
					case r.Method == http.MethodDelete && strings.Contains(r.URL.RawQuery, "uploadId=contract-upload-id"):
						recorder.aborted = true
						w.WriteHeader(http.StatusNoContent)
					default:
						t.Fatalf("unexpected AWS multipart request: %s %s", r.Method, r.URL.String())
					}
				})

				return &awsPartUploader{client: client, bucketName: testBucketName, objectName: testObjectName}
			},
		},
	})
}

type completeMultipartUploadRequest struct {
	Parts []struct {
		PartNumber int `xml:"PartNumber"`
	} `xml:"Part"`
}

func newTestS3Client(t *testing.T, handler http.HandlerFunc) *s3.Client {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return s3.NewFromConfig(aws.Config{
		Credentials:  credentials.NewStaticCredentialsProvider("test", "test", ""),
		Region:       "us-east-1",
		BaseEndpoint: aws.String(server.URL),
		HTTPClient:   server.Client(),
	}, func(o *s3.Options) {
		o.UsePathStyle = true
	})
}
