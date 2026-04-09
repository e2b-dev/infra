package teamprovision

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	sharedteamprovision "github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

const billingServerAPIKeyHeader = "X-Billing-Server-API-Key"

type HTTPProvisionSink struct {
	baseURL  string
	apiToken string
	client   *http.Client
}

var _ TeamProvisionSink = (*HTTPProvisionSink)(nil)

type errorResponse struct {
	Message string `json:"message"`
}

func NewHTTPProvisionSink(baseURL, apiToken string, timeout time.Duration) *HTTPProvisionSink {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}

	return &HTTPProvisionSink{
		baseURL:  strings.TrimRight(baseURL, "/"),
		apiToken: apiToken,
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

func (s *HTTPProvisionSink) ProvisionTeam(ctx context.Context, req sharedteamprovision.TeamBillingProvisionRequestedV1) error {
	if s.baseURL == "" || s.apiToken == "" {
		return &ProvisionError{
			StatusCode: http.StatusServiceUnavailable,
			Message:    "billing provisioning sink is not configured",
		}
	}

	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal billing provisioning request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		s.baseURL+"/internal/teams/provision",
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("create billing provisioning request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(billingServerAPIKeyHeader, s.apiToken)

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("call billing provisioning endpoint: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return nil
	}

	message, err := readProvisionErrorMessage(resp)
	if err != nil {
		return fmt.Errorf("read billing provisioning error response: %w", err)
	}

	return &ProvisionError{
		StatusCode: resp.StatusCode,
		Message:    message,
	}
}

func readProvisionErrorMessage(resp *http.Response) (string, error) {
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if err != nil {
		return "", err
	}

	var apiErr errorResponse
	if err := json.Unmarshal(body, &apiErr); err == nil && apiErr.Message != "" {
		return apiErr.Message, nil
	}

	message := strings.TrimSpace(string(body))
	if message != "" {
		return message, nil
	}

	return http.StatusText(resp.StatusCode), nil
}
