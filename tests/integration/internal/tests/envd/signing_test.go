package envd

import (
	"context"
	"fmt"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	envdapi "github.com/e2b-dev/infra/tests/integration/internal/envd/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/stretchr/testify/assert"
	"net/http"
	"testing"
	"time"
)

func TestDownloadFileWhenAuthIsDisabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sbx := createSandbox(t, setup.WithAPIKey())
	envdClient := setup.GetEnvdClient(t, ctx)

	sandboxEnvdInitCall(t, ctx, envdInitCall{
		sbx:                   sbx,
		client:                envdClient,
		body:                  envdapi.PostInitJSONRequestBody{},
		expectedResErr:        nil,
		expectedResHttpStatus: http.StatusNoContent,
	})

	// create test file
	filePath := "test.txt"
	textFile, contentType := createTextFile(t, filePath, "Hello, World!")

	writeRes, err := envdClient.HTTPClient.PostFilesWithBodyWithResponse(
		ctx,
		&envdapi.PostFilesParams{
			Path:     &filePath,
			Username: "user",
		},
		contentType,
		textFile,
		setup.WithSandbox(sbx.JSON201.SandboxID, sbx.JSON201.ClientID),
	)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, writeRes.StatusCode())

	getRes, err := envdClient.HTTPClient.GetFilesWithResponse(
		ctx,
		&envdapi.GetFilesParams{Path: &filePath, Username: "user"},
		setup.WithSandbox(sbx.JSON201.SandboxID, sbx.JSON201.ClientID),
	)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, getRes.StatusCode())
}

func TestDownloadFileWithoutSigningWhenAuthIsEnabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sbx := createSandbox(t, setup.WithAPIKey())
	envdClient := setup.GetEnvdClient(t, ctx)
	envdToken := "secret-token"

	sandboxEnvdInitCall(t, ctx, envdInitCall{
		sbx:                   sbx,
		client:                envdClient,
		body:                  envdapi.PostInitJSONRequestBody{AccessToken: &envdToken},
		expectedResErr:        nil,
		expectedResHttpStatus: http.StatusNoContent,
	})

	// create test file
	filePath := "test.txt"
	textFile, contentType := createTextFile(t, filePath, "Hello, World!")

	writeFileSigning := buildSigningKey(filePath, "user", envdToken, "write")
	writeRes, err := envdClient.HTTPClient.PostFilesWithBodyWithResponse(
		ctx,
		&envdapi.PostFilesParams{
			Path:     &filePath,
			Username: "user",
			Signing:  &writeFileSigning,
		},
		contentType,
		textFile,
		setup.WithSandbox(sbx.JSON201.SandboxID, sbx.JSON201.ClientID),
		setup.WithAccessToken(envdToken),
	)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, writeRes.StatusCode())

	readRes, readErr := envdClient.HTTPClient.GetFiles(
		ctx,
		&envdapi.GetFilesParams{Path: &filePath, Username: "user"},
		setup.WithSandbox(sbx.JSON201.SandboxID, sbx.JSON201.ClientID),
	)

	assert.NoError(t, readErr)
	assert.Equal(t, http.StatusUnauthorized, readRes.StatusCode)
}

func TestDownloadFileWithSigningWhenAuthIsEnabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sbx := createSandbox(t, setup.WithAPIKey())
	envdClient := setup.GetEnvdClient(t, ctx)
	envdToken := "secret-token"

	time.Sleep(2 * time.Second) // wait for the signing key to be generated

	sandboxEnvdInitCall(t, ctx, envdInitCall{
		sbx:                   sbx,
		client:                envdClient,
		body:                  envdapi.PostInitJSONRequestBody{AccessToken: &envdToken},
		expectedResErr:        nil,
		expectedResHttpStatus: http.StatusNoContent,
	})

	// create test file
	filePath := "test.txt"
	readFileSigning := buildSigningKey(filePath, "user", envdToken, "read")
	writeFileSigning := buildSigningKey(filePath, "user", envdToken, "write")
	textFile, contentType := createTextFile(t, filePath, "Hello, World!")

	writeRes, err := envdClient.HTTPClient.PostFilesWithBodyWithResponse(
		ctx,
		&envdapi.PostFilesParams{
			Path:     &filePath,
			Username: "user",
			Signing:  &writeFileSigning,
		},
		contentType,
		textFile,
		setup.WithSandbox(sbx.JSON201.SandboxID, sbx.JSON201.ClientID),
	)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, writeRes.StatusCode())

	readRes, readErr := envdClient.HTTPClient.GetFilesWithResponse(
		ctx,
		&envdapi.GetFilesParams{Path: &filePath, Username: "user", Signing: &readFileSigning},
		setup.WithSandbox(sbx.JSON201.SandboxID, sbx.JSON201.ClientID),
	)

	assert.NoError(t, readErr)
	assert.Equal(t, http.StatusOK, readRes.StatusCode())
	assert.Equal(t, "Hello, World!", string(readRes.Body))
}

func buildSigningKey(path string, username string, accessToken string, operation string) string {
	hasher := keys.NewSHA256Hashing()
	signing := fmt.Sprintf("%s:%s:%s:%s", path, operation, username, accessToken)
	signingWithVersionPrefix := fmt.Sprintf("v1_%s", hasher.Hash([]byte(signing)))

	return signingWithVersionPrefix
}
