package host

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/e2b-dev/infra/packages/envd/internal/utils"
)

const (
	E2BRunDir = "/run/e2b" // store sandbox metadata files here

	mmdsDefaultAddress  = "169.254.169.254"
	mmdsTokenExpiration = 60 * time.Second

	mmdsAccessTokenRequestClientTimeout = 10 * time.Second
)

var mmdsAccessTokenClient = &http.Client{
	Timeout: mmdsAccessTokenRequestClientTimeout,
	Transport: &http.Transport{
		DisableKeepAlives: true,
	},
}

type MMDSOpts struct {
	SandboxID            string `json:"instanceID"`
	TemplateID           string `json:"envID"`
	LogsCollectorAddress string `json:"address"`
	AccessTokenHash      string `json:"accessTokenHash"`
}

func (opts *MMDSOpts) Update(sandboxID, templateID, collectorAddress string) {
	opts.SandboxID = sandboxID
	opts.TemplateID = templateID
	opts.LogsCollectorAddress = collectorAddress
}

func (opts *MMDSOpts) AddOptsToJSON(jsonLogs []byte) ([]byte, error) {
	parsed := make(map[string]any)

	err := json.Unmarshal(jsonLogs, &parsed)
	if err != nil {
		return nil, err
	}

	parsed["instanceID"] = opts.SandboxID
	parsed["envID"] = opts.TemplateID

	data, err := json.Marshal(parsed)

	return data, err
}

func getMMDSToken(ctx context.Context, client *http.Client) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://"+mmdsDefaultAddress+"/latest/api/token", &bytes.Buffer{})
	if err != nil {
		return "", err
	}

	request.Header["X-metadata-token-ttl-seconds"] = []string{fmt.Sprint(mmdsTokenExpiration.Seconds())}

	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return "", err
	}

	token := string(body)

	if len(token) == 0 {
		return "", fmt.Errorf("mmds token is an empty string")
	}

	return token, nil
}

func getMMDSOpts(ctx context.Context, client *http.Client, token string) (*MMDSOpts, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+mmdsDefaultAddress, &bytes.Buffer{})
	if err != nil {
		return nil, err
	}

	request.Header["X-metadata-token"] = []string{token}
	request.Header["Accept"] = []string{"application/json"}

	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}

	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}

	var opts MMDSOpts

	err = json.Unmarshal(body, &opts)
	if err != nil {
		return nil, err
	}

	return &opts, nil
}

// GetAccessTokenHashFromMMDS reads the access token hash from MMDS.
// This is used to validate that /init requests come from the orchestrator.
func GetAccessTokenHashFromMMDS(ctx context.Context) (string, error) {
	token, err := getMMDSToken(ctx, mmdsAccessTokenClient)
	if err != nil {
		return "", fmt.Errorf("failed to get MMDS token: %w", err)
	}

	opts, err := getMMDSOpts(ctx, mmdsAccessTokenClient, token)
	if err != nil {
		return "", fmt.Errorf("failed to get MMDS opts: %w", err)
	}

	return opts.AccessTokenHash, nil
}

func PollForMMDSOpts(ctx context.Context, mmdsChan chan<- *MMDSOpts, envVars *utils.Map[string, string]) {
	httpClient := &http.Client{}
	defer httpClient.CloseIdleConnections()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintf(os.Stderr, "context cancelled while waiting for mmds opts")

			return
		case <-ticker.C:
			token, err := getMMDSToken(ctx, httpClient)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error getting mmds token: %v\n", err)

				continue
			}

			mmdsOpts, err := getMMDSOpts(ctx, httpClient, token)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error getting mmds opts: %v\n", err)

				continue
			}

			envVars.Store("E2B_SANDBOX_ID", mmdsOpts.SandboxID)
			envVars.Store("E2B_TEMPLATE_ID", mmdsOpts.TemplateID)

			if err := os.WriteFile(filepath.Join(E2BRunDir, ".E2B_SANDBOX_ID"), []byte(mmdsOpts.SandboxID), 0o666); err != nil {
				fmt.Fprintf(os.Stderr, "error writing sandbox ID file: %v\n", err)
			}
			if err := os.WriteFile(filepath.Join(E2BRunDir, ".E2B_TEMPLATE_ID"), []byte(mmdsOpts.TemplateID), 0o666); err != nil {
				fmt.Fprintf(os.Stderr, "error writing template ID file: %v\n", err)
			}

			if mmdsOpts.LogsCollectorAddress != "" {
				mmdsChan <- mmdsOpts
			}

			return
		}
	}
}
