package host

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/e2b-dev/infra/packages/envd/internal/utils"
)

const (
	E2BRunDir = "/run/e2b"

	mmdsDefaultAddress  = "169.254.169.254"
	mmdsTokenExpiration = 60 * time.Second
)

type MMDSOpts struct {
	TraceID   string `json:"traceID"`
	SandboxID string `json:"instanceID"`
	EnvID     string `json:"envID"`
	Address   string `json:"address"`
	TeamID    string `json:"teamID"`
}

func (opts *MMDSOpts) AddOptsToJSON(jsonLogs []byte) ([]byte, error) {
	parsed := make(map[string]interface{})

	err := json.Unmarshal(jsonLogs, &parsed)
	if err != nil {
		return nil, err
	}

	parsed["instanceID"] = opts.SandboxID
	parsed["envID"] = opts.EnvID
	parsed["traceID"] = opts.TraceID
	parsed["teamID"] = opts.TeamID

	data, err := json.Marshal(parsed)

	return data, err
}

func getMMDSToken(ctx context.Context, client *http.Client) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://"+mmdsDefaultAddress+"/latest/api/token", new(bytes.Buffer))
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
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+mmdsDefaultAddress, new(bytes.Buffer))
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

func PollForMMDSOpts(ctx context.Context, mmdsChan chan<- *MMDSOpts, envVars *utils.Map[string, string]) {
	httpClient := &http.Client{}
	defer httpClient.CloseIdleConnections()

	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			fmt.Fprintf(os.Stderr, "context cancelled while waiting for mmds opts")
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

			if mmdsOpts.Address != "" {
				envVars.Store("E2B_SANDBOX_ID", mmdsOpts.SandboxID)
				envVars.Store("E2B_ENV_ID", mmdsOpts.EnvID)
				envVars.Store("E2B_TEAM_ID", mmdsOpts.TeamID)
				if err := os.WriteFile(E2BRunDir+"/.E2B_SANDBOX_ID", []byte(mmdsOpts.SandboxID), 0444); err != nil {
					fmt.Fprintf(os.Stderr, "error writing sandbox ID file: %v\n", err)
				}
				if err := os.WriteFile(E2BRunDir+"/.E2B_ENV_ID", []byte(mmdsOpts.EnvID), 0444); err != nil {
					fmt.Fprintf(os.Stderr, "error writing env ID file: %v\n", err)
				}
				if err := os.WriteFile(E2BRunDir+"/.E2B_TEAM_ID", []byte(mmdsOpts.TeamID), 0444); err != nil {
					fmt.Fprintf(os.Stderr, "error writing team ID file: %v\n", err)
				}
				mmdsChan <- mmdsOpts

				return
			}
		}
	}
}
