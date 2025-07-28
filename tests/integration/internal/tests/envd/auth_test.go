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

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/filesystem"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/envd/process"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	envdapi "github.com/e2b-dev/infra/tests/integration/internal/envd/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func createSandbox(t *testing.T, sbxWithAuth bool, reqEditors ...api.RequestEditorFn) *api.PostSandboxesResponse {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()

	sbxTimeout := int32(10)
	resp, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
		TemplateID: setup.SandboxTemplateID,
		Timeout:    &sbxTimeout,
		Secure:     &sbxWithAuth,
	}, reqEditors...)

	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, resp.StatusCode())

	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Response: %s", string(resp.Body))
		}

		if resp.JSON201 != nil {
			utils.TeardownSandbox(t, setup.GetAPIClient(), resp.JSON201.SandboxID)
		}
	})

	return resp
}

func TestAccessToAuthorizedPathWithoutToken(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	sbx := createSandbox(t, true, setup.WithAPIKey())
	require.NotNil(t, sbx.JSON201)
	require.NotNil(t, sbx.JSON201.EnvdAccessToken)

	envdClient := setup.GetEnvdClient(t, ctx)

	// set up the request to list the directory
	req := connect.NewRequest(&filesystem.ListDirRequest{Path: "/"})
	setup.SetSandboxHeader(req.Header(), sbx.JSON201.SandboxID)
	setup.SetUserHeader(req.Header(), "user")

	_, err := envdClient.FilesystemClient.ListDir(ctx, req)
	require.Error(t, err)

	assert.Equal(t, err.Error(), "unauthenticated: 401 Unauthorized")
}

func TestRunUnauthorizedInitWithAlreadySecuredEnvd(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	sbx := createSandbox(t, true, setup.WithAPIKey())
	require.NotNil(t, sbx.JSON201)
	require.NotNil(t, sbx.JSON201.EnvdAccessToken)

	envdClient := setup.GetEnvdClient(t, ctx)

	sandboxEnvdInitCall(t, ctx, envdInitCall{
		sbx:                   sbx,
		client:                envdClient,
		body:                  envdapi.PostInitJSONRequestBody{},
		expectedResErr:        nil,
		expectedResHttpStatus: http.StatusUnauthorized,
	})
}

func TestChangeAccessAuthorizedToken(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	sbx := createSandbox(t, true, setup.WithAPIKey())
	require.NotNil(t, sbx.JSON201)
	require.NotNil(t, sbx.JSON201.EnvdAccessToken)

	envdClient := setup.GetEnvdClient(t, ctx)
	envdAuthTokenA := sbx.JSON201.EnvdAccessToken
	envdAuthTokenB := "second-token"

	sandboxEnvdInitCall(t, ctx, envdInitCall{
		sbx:                   sbx,
		client:                envdClient,
		authToken:             envdAuthTokenA, // this is the old token used currently by envd
		body:                  envdapi.PostInitJSONRequestBody{AccessToken: &envdAuthTokenB},
		expectedResErr:        nil,
		expectedResHttpStatus: http.StatusConflict,
	})
}

func TestAccessAuthorizedPathWithResumedSandboxWithValidAccessToken(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	sbx := createSandbox(t, true, setup.WithAPIKey())
	require.NotNil(t, sbx.JSON201)
	require.NotNil(t, sbx.JSON201.EnvdAccessToken)

	envdClient := setup.GetEnvdClient(t, ctx)
	sbxMeta := sbx.JSON201

	// set up the request to list the directory
	req := connect.NewRequest(&filesystem.ListDirRequest{Path: "/"})
	setup.SetSandboxHeader(req.Header(), sbxMeta.SandboxID)
	setup.SetUserHeader(req.Header(), "user")
	setup.SetAccessTokenHeader(req.Header(), *sbxMeta.EnvdAccessToken)

	filePath := "demo.txt"
	fileContent := "Hello, world!"

	// create a test file
	utils.UploadFile(t, ctx, sbxMeta, envdClient, filePath, fileContent)

	c := setup.GetAPIClient()

	// stop sandbox
	_, err := c.PostSandboxesSandboxIDPauseWithResponse(ctx, sbxMeta.SandboxID, setup.WithAPIKey())
	if err != nil {
		t.Fatal(err)
	}

	sbxIdWithClient := sbxMeta.SandboxID + "-" + sbxMeta.ClientID

	// resume sandbox
	sbxResume, err := c.PostSandboxesSandboxIDResumeWithResponse(ctx, sbxIdWithClient, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
	if err != nil {
		t.Fatal(err)
	}

	require.Equal(t, http.StatusCreated, sbxResume.StatusCode())

	// try to get the file with the valid access token
	fileResponse, err := envdClient.HTTPClient.GetFilesWithResponse(
		ctx,
		&envdapi.GetFilesParams{Path: &filePath, Username: "user"},
		setup.WithSandbox(sbx.JSON201.SandboxID),
		setup.WithEnvdAccessToken(*sbxMeta.EnvdAccessToken),
	)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, http.StatusOK, fileResponse.StatusCode())
	assert.Equal(t, fileContent, string(fileResponse.Body))
}

func TestAccessAuthorizedPathWithResumedSandboxWithoutAccessToken(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	sbx := createSandbox(t, true, setup.WithAPIKey())
	require.NotNil(t, sbx.JSON201)
	require.NotNil(t, sbx.JSON201.EnvdAccessToken)

	envdClient := setup.GetEnvdClient(t, ctx)
	sbxMeta := sbx.JSON201

	// set up the request to list the directory
	req := connect.NewRequest(&filesystem.ListDirRequest{Path: "/"})
	setup.SetSandboxHeader(req.Header(), sbxMeta.SandboxID)
	setup.SetUserHeader(req.Header(), "user")
	setup.SetAccessTokenHeader(req.Header(), *sbxMeta.EnvdAccessToken)

	filePath := "demo.txt"
	fileContent := "Hello, world!"

	// create a test file
	utils.UploadFile(t, ctx, sbxMeta, envdClient, filePath, fileContent)

	c := setup.GetAPIClient()

	// stop sandbox
	_, err := c.PostSandboxesSandboxIDPauseWithResponse(ctx, sbxMeta.SandboxID, setup.WithAPIKey())
	if err != nil {
		t.Fatal(err)
	}

	sbxIdWithClient := sbxMeta.SandboxID + "-" + sbxMeta.ClientID

	// resume sandbox
	sbxResume, err := c.PostSandboxesSandboxIDResumeWithResponse(ctx, sbxIdWithClient, api.PostSandboxesSandboxIDResumeJSONRequestBody{}, setup.WithAPIKey())
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, http.StatusCreated, sbxResume.StatusCode())

	// try to get the file with the without access token
	fileResponse, err := envdClient.HTTPClient.GetFilesWithResponse(
		ctx,
		&envdapi.GetFilesParams{Path: &filePath, Username: "user"},
		setup.WithSandbox(sbx.JSON201.SandboxID),
	)
	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, http.StatusUnauthorized, fileResponse.StatusCode())
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
	envdReqSetup := []envdapi.RequestEditorFn{setup.WithSandbox(req.sbx.JSON201.SandboxID)}
	if req.authToken != nil {
		envdReqSetup = append(envdReqSetup, setup.WithEnvdAccessToken(*req.authToken))
	}

	res, err := req.client.HTTPClient.PostInitWithResponse(ctx, req.body, envdReqSetup...)
	if req.expectedResErr != nil {
		assert.Equal(t, *req.expectedResErr, err)
	} else {
		assert.NoError(t, err)
	}

	assert.Equal(t, req.expectedResHttpStatus, res.StatusCode())

	if res.StatusCode() == http.StatusBadGateway {
		logs, err := getSandboxLogs(t, ctx, req.client, req.sbx)
		if err != nil {
			t.Logf("Failed to get logs from sandbox %s: %s", req.sbx.JSON201.SandboxID, err)
		} else {
			t.Logf("Sandbox logs for the failed (502) request to sandbox %s:\n%s", req.sbx.JSON201.SandboxID, logs)
		}
	}
}

func getSandboxLogs(t *testing.T, ctx context.Context, client *setup.EnvdClient, sbx *api.PostSandboxesResponse) (string, error) {
	req := connect.NewRequest(&process.StartRequest{
		Process: &process.ProcessConfig{
			Cmd:  "journalctl",
			Args: []string{"-u", "envd"},
		},
	})
	setup.SetSandboxHeader(req.Header(), sbx.JSON201.SandboxID)
	setup.SetUserHeader(req.Header(), "root")

	serverCtx, serverCancel := context.WithCancel(ctx)
	defer serverCancel()

	stream, err := client.ProcessClient.Start(serverCtx, req)
	if err != nil {
		return "", fmt.Errorf("failed to get logs from sandbox: %w", err)
	}

	defer func() {
		serverCancel()
		streamErr := stream.Close()
		if streamErr != nil {
			t.Logf("Error closing stream: %v", streamErr)
		}
	}()

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
