package envd

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	envdapi "github.com/e2b-dev/infra/tests/integration/internal/envd/api"
	"github.com/e2b-dev/infra/tests/integration/internal/envd/filesystem"
	"github.com/e2b-dev/infra/tests/integration/internal/envd/process"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

func createSandbox(t *testing.T, sbxWithAuth bool, reqEditors ...api.RequestEditorFn) *api.PostSandboxesResponse {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := setup.GetAPIClient()

	sbxTimeout := int32(10)

	resp, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
		TemplateID: setup.SandboxTemplateID,
		Timeout:    &sbxTimeout,
		Secure:     &sbxWithAuth,
	}, reqEditors...)

	assert.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode())

	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Response: %s", string(resp.Body))
		}
	})

	return resp
}

func TestAccessToAuthorizedPathWithoutToken(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sbx := createSandbox(t, true, setup.WithAPIKey())
	assert.NotNil(t, sbx.JSON201)
	assert.NotNil(t, sbx.JSON201.EnvdAccessToken)

	envdClient := setup.GetEnvdClient(t, ctx)

	// set up the request to list the directory
	req := connect.NewRequest(&filesystem.ListDirRequest{Path: "/"})
	setup.SetSandboxHeader(req.Header(), sbx.JSON201.SandboxID, sbx.JSON201.ClientID)
	setup.SetUserHeader(req.Header(), "user")

	_, err := envdClient.FilesystemClient.ListDir(ctx, req)
	assert.NotNil(t, err)
	require.Error(t, err)
	assert.Equal(t, err.Error(), "unauthenticated: 401 Unauthorized")
}

func TestRunUnauthorizedInitWithAlreadySecuredEnvd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sbx := createSandbox(t, true, setup.WithAPIKey())
	assert.NotNil(t, sbx.JSON201)
	assert.NotNil(t, sbx.JSON201.EnvdAccessToken)

	envdClient := setup.GetEnvdClient(t, ctx)

	// second call without authorization
	sandboxEnvdInitCall(t, ctx, envdInitCall{
		sbx:                   sbx,
		client:                envdClient,
		body:                  envdapi.PostInitJSONRequestBody{},
		expectedResErr:        nil,
		expectedResHttpStatus: http.StatusUnauthorized,
	})
}

func TestAccessAuthorizedPathWithOutdatedAccessToken(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sbx := createSandbox(t, true, setup.WithAPIKey())
	assert.NotNil(t, sbx.JSON201)
	assert.NotNil(t, sbx.JSON201.EnvdAccessToken)

	envdClient := setup.GetEnvdClient(t, ctx)
	envdAuthTokenA := sbx.JSON201.EnvdAccessToken
	envdAuthTokenB := "second-token"

	// set up the request to list the directory
	req := connect.NewRequest(&filesystem.ListDirRequest{Path: "/"})
	setup.SetSandboxHeader(req.Header(), sbx.JSON201.SandboxID, sbx.JSON201.ClientID)
	setup.SetUserHeader(req.Header(), "user")
	setup.SetAccessTokenHeader(req.Header(), *envdAuthTokenA)

	_, err := envdClient.FilesystemClient.ListDir(ctx, req)
	assert.NoError(t, err)

	// second init call with different secret
	sandboxEnvdInitCall(t, ctx, envdInitCall{
		sbx:                   sbx,
		client:                envdClient,
		authToken:             envdAuthTokenA, // this is the old token used currently by envd
		body:                  envdapi.PostInitJSONRequestBody{AccessToken: &envdAuthTokenB},
		expectedResErr:        nil,
		expectedResHttpStatus: http.StatusNoContent,
	})

	// try to call with old access token
	req = connect.NewRequest(&filesystem.ListDirRequest{Path: "/"})
	setup.SetSandboxHeader(req.Header(), sbx.JSON201.SandboxID, sbx.JSON201.ClientID)
	setup.SetUserHeader(req.Header(), "user")
	setup.SetAccessTokenHeader(req.Header(), *envdAuthTokenA)

	_, err = envdClient.FilesystemClient.ListDir(ctx, req)
	require.Error(t, err)
	assert.Equal(t, err.Error(), "unauthenticated: 401 Unauthorized")
}

type envdInitCall struct {
	sbx                   *api.PostSandboxesResponse
	client                *setup.EnvdClient
	body                  envdapi.PostInitJSONRequestBody
	authToken             *string
	expectedResErr        *error
	expectedResHttpStatus int
}

func sandboxEnvdInitCall(t *testing.T, ctx context.Context, req envdInitCall) {
	envdReqSetup := []envdapi.RequestEditorFn{setup.WithSandbox(req.sbx.JSON201.SandboxID, req.sbx.JSON201.ClientID)}
	if req.authToken != nil {
		envdReqSetup = append(envdReqSetup, setup.WithAccessToken(*req.authToken))
	}

	res, err := req.client.HTTPClient.PostInitWithResponse(ctx, req.body, envdReqSetup...)
	if req.expectedResErr != nil {
		assert.Equal(t, *req.expectedResErr, err)
	} else {
		assert.NoError(t, err)
	}

	assert.Equal(t, req.expectedResHttpStatus, res.StatusCode())

	if res.StatusCode() == http.StatusBadGateway {
		logs, err := getSandboxLogs(ctx, req.client, req.sbx)
		if err != nil {
			t.Logf("Failed to get logs from sandbox %s: %s", req.sbx.JSON201.SandboxID, err)
		} else {
			t.Logf("Sandbox logs for the failed (502) request to sandbox %s:\n%s", req.sbx.JSON201.SandboxID, logs)
		}
	}
}

func getSandboxLogs(ctx context.Context, client *setup.EnvdClient, sbx *api.PostSandboxesResponse) (string, error) {
	req := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{
			Cmd:  "journalctl",
			Args: []string{"-u", "envd"},
		},
	})
	setup.SetSandboxHeader(req.Header(), sbx.JSON201.SandboxID, sbx.JSON201.ClientID)
	setup.SetUserHeader(req.Header(), "root")
	stream, err := client.ProcessClient.Start(
		ctx,
		req,
	)
	if err != nil {
		return "", fmt.Errorf("failed to get logs from sandbox: %w", err)
	}

	defer stream.Close()

	out := []string{}

	for stream.Receive() {
		msg := stream.Msg()

		if msg.Event.GetData() != nil {
			switch data := msg.Event.GetData().GetOutput().(type) {
			case *process.ProcessEvent_DataEvent_Stdout:
				out = append(out, string(data.Stdout))
			case *process.ProcessEvent_DataEvent_Stderr:
				out = append(out, string(data.Stderr))
			}
		}
	}

	return strings.Join(out, ""), nil
}
