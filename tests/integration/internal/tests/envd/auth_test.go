package envd

import (
	"connectrpc.com/connect"
	"context"
	envdapi "github.com/e2b-dev/infra/tests/integration/internal/envd/api"
	"github.com/e2b-dev/infra/tests/integration/internal/envd/filesystem"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/stretchr/testify/assert"
	"net/http"
	"testing"
)

func TestAccessAuthorizedPath(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sbx := createSandbox(t, setup.WithAPIKey())

	envdClient := setup.GetEnvdClient(t, ctx)
	envdAuthToken := "test"

	// set up the auth token
	initResp, err := envdClient.HTTPClient.PostInitWithResponse(
		ctx,
		envdapi.PostInitJSONRequestBody{AccessToken: &envdAuthToken},
		setup.WithSandbox(sbx.JSON201.SandboxID, sbx.JSON201.ClientID),
	)

	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, http.StatusNoContent, initResp.StatusCode())

	// set up the request to list the directory
	req := connect.NewRequest(&filesystem.ListDirRequest{Path: "/"})
	setup.SetSandboxHeader(req.Header(), sbx.JSON201.SandboxID, sbx.JSON201.ClientID)
	setup.SetUserHeader(req.Header(), "user")

	_, err = envdClient.FilesystemClient.ListDir(ctx, req)
	assert.Errorf(t, err, "unauthenticated: invalid access token")
}

func TestAccessAuthorizedPathWithChangedAccessToken(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sbx := createSandbox(t, setup.WithAPIKey())

	envdClient := setup.GetEnvdClient(t, ctx)
	envdAuthTokenA := "test"
	envdAuthTokenB := "hello"

	// set up the auth token
	initResp, err := envdClient.HTTPClient.PostInitWithResponse(
		ctx,
		envdapi.PostInitJSONRequestBody{AccessToken: &envdAuthTokenA},
		setup.WithSandbox(sbx.JSON201.SandboxID, sbx.JSON201.ClientID),
	)

	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, http.StatusNoContent, initResp.StatusCode())

	// set up the request to list the directory
	req := connect.NewRequest(&filesystem.ListDirRequest{Path: "/"})
	setup.SetSandboxHeader(req.Header(), sbx.JSON201.SandboxID, sbx.JSON201.ClientID)
	setup.SetUserHeader(req.Header(), "user")
	setup.SetAccessTokenHeader(req.Header(), envdAuthTokenA)

	_, err = envdClient.FilesystemClient.ListDir(ctx, req)
	assert.NoError(t, err)

	// change access token in run
	// set up the auth token
	initResp, err = envdClient.HTTPClient.PostInitWithResponse(
		ctx,
		envdapi.PostInitJSONRequestBody{AccessToken: &envdAuthTokenB},
		setup.WithAccessToken(envdAuthTokenA),
		setup.WithSandbox(sbx.JSON201.SandboxID, sbx.JSON201.ClientID),
	)

	if err != nil {
		t.Fatal(err)
	}

	assert.Equal(t, http.StatusNoContent, initResp.StatusCode())

	// try to call with old access token
	req = connect.NewRequest(&filesystem.ListDirRequest{Path: "/"})
	setup.SetSandboxHeader(req.Header(), sbx.JSON201.SandboxID, sbx.JSON201.ClientID)
	setup.SetUserHeader(req.Header(), "user")
	setup.SetAccessTokenHeader(req.Header(), envdAuthTokenA)

	_, err = envdClient.FilesystemClient.ListDir(ctx, req)
	assert.Errorf(t, err, "unauthenticated: invalid access token")
}
