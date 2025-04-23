package envd

import (
	"context"
	"fmt"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	envdapi "github.com/e2b-dev/infra/tests/integration/internal/envd/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/stretchr/testify/assert"
	"net/http"
	"strconv"
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

	writeFileSigning := generateSignature(filePath, "user", "write", nil, envdToken)
	writeRes, err := envdClient.HTTPClient.PostFilesWithBodyWithResponse(
		ctx,
		&envdapi.PostFilesParams{
			Path:      &filePath,
			Username:  "user",
			Signature: &writeFileSigning,
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

	sandboxEnvdInitCall(t, ctx, envdInitCall{
		sbx:                   sbx,
		client:                envdClient,
		body:                  envdapi.PostInitJSONRequestBody{AccessToken: &envdToken},
		expectedResErr:        nil,
		expectedResHttpStatus: http.StatusNoContent,
	})

	// create test file
	filePath := "test.txt"
	readFileSigning := generateSignature(filePath, "user", "read", nil, envdToken)
	writeFileSigning := generateSignature(filePath, "user", "write", nil, envdToken)
	textFile, contentType := createTextFile(t, filePath, "Hello, World!")

	writeRes, err := envdClient.HTTPClient.PostFilesWithBodyWithResponse(
		ctx,
		&envdapi.PostFilesParams{
			Path:      &filePath,
			Username:  "user",
			Signature: &writeFileSigning,
		},
		contentType,
		textFile,
		setup.WithSandbox(sbx.JSON201.SandboxID, sbx.JSON201.ClientID),
	)

	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, writeRes.StatusCode())

	readRes, readErr := envdClient.HTTPClient.GetFilesWithResponse(
		ctx,
		&envdapi.GetFilesParams{Path: &filePath, Username: "user", Signature: &readFileSigning},
		setup.WithSandbox(sbx.JSON201.SandboxID, sbx.JSON201.ClientID),
	)

	assert.NoError(t, readErr)
	assert.Equal(t, http.StatusOK, readRes.StatusCode())
	assert.Equal(t, "Hello, World!", string(readRes.Body))
}

func TestDownloadWithAlreadyExpiredToken(t *testing.T) {
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
	filePath := "demo/test.txt"
	signatureExpiration := time.Now().Add(-3 * time.Hour).Unix()
	signatureForRead := generateSignature(filePath, "user", "read", &signatureExpiration, envdToken)

	readExpiration := int(signatureExpiration)
	readRes, readErr := envdClient.HTTPClient.GetFilesWithResponse(
		ctx,
		&envdapi.GetFilesParams{
			Path:                &filePath,
			Username:            "user",
			Signature:           &signatureForRead,
			SignatureExpiration: &readExpiration,
		},
		setup.WithSandbox(sbx.JSON201.SandboxID, sbx.JSON201.ClientID),
	)

	assert.NoError(t, readErr)
	assert.Equal(t, http.StatusUnauthorized, readRes.StatusCode())
	assert.Equal(t, "{\"code\":401,\"message\":\"signature is already expired\"}\n", string(readRes.Body))
}

func TestDownloadWithHealthyToken(t *testing.T) {
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
	filePath := "demo/test.txt"
	signatureExpiration := time.Now().Add(1 * time.Minute).Unix()
	signatureForRead := generateSignature(filePath, "user", "read", &signatureExpiration, envdToken)

	readExpiration := int(signatureExpiration)
	readRes, readErr := envdClient.HTTPClient.GetFilesWithResponse(
		ctx,
		&envdapi.GetFilesParams{
			Path:                &filePath,
			Username:            "user",
			Signature:           &signatureForRead,
			SignatureExpiration: &readExpiration,
		},
		setup.WithSandbox(sbx.JSON201.SandboxID, sbx.JSON201.ClientID),
	)

	assert.NoError(t, readErr)
	assert.Equal(t, http.StatusNotFound, readRes.StatusCode())
	assert.Equal(t, "{\"code\":404,\"message\":\"path '/home/user/demo/test.txt' does not exist\"}\n", string(readRes.Body))
}

func TestAccessWithNotCorrespondingSignatureAndSignatureExpiration(t *testing.T) {
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
	filePath := "demo/test.txt"
	signatureExpiration := time.Now().Add(-1 * time.Minute).Unix()
	signatureForRead := generateSignature(filePath, "user", "read", nil, envdToken)

	readExpiration := int(signatureExpiration)
	readRes, readErr := envdClient.HTTPClient.GetFilesWithResponse(
		ctx,
		&envdapi.GetFilesParams{
			Path:                &filePath,
			Username:            "user",
			Signature:           &signatureForRead,
			SignatureExpiration: &readExpiration,
		},
		setup.WithSandbox(sbx.JSON201.SandboxID, sbx.JSON201.ClientID),
	)

	assert.NoError(t, readErr)
	assert.Equal(t, http.StatusUnauthorized, readRes.StatusCode())
	assert.Equal(t, "{\"code\":401,\"message\":\"invalid signature\"}\n", string(readRes.Body))
}

func generateSignature(path string, username string, operation string, signatureExpiration *int64, accessToken string) string {
	var signature string
	hasher := keys.NewSHA256Hashing()

	if signatureExpiration == nil {
		signature = fmt.Sprintf("%s:%s:%s:%s", path, operation, username, accessToken)
	} else {
		signature = fmt.Sprintf("%s:%s:%s:%s:%s", path, operation, username, accessToken, strconv.FormatInt(*signatureExpiration, 10))
	}

	return fmt.Sprintf("v1_%s", hasher.HashWithoutPrefix([]byte(signature)))
}
