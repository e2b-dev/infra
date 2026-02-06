package envd

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	sharedUtils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
	"github.com/e2b-dev/infra/tests/integration/internal/envd"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestDownloadFileWhenAuthIsDisabled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	sbx := createSandbox(t, false, setup.WithAPIKey())
	envdClient := setup.GetEnvdClient(t, ctx)

	// create test file
	filePath := "test.txt"
	textFile, contentType := utils.CreateTextFile(t, filePath, "Hello, World!")

	writeRes, err := envdClient.HTTPClient.PostFilesWithBodyWithResponse(
		ctx,
		&envd.PostFilesParams{
			Path:     &filePath,
			Username: sharedUtils.ToPtr("user"),
		},
		contentType,
		textFile,
		setup.WithSandbox(sbx.JSON201.SandboxID),
	)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, writeRes.StatusCode())

	getRes, err := envdClient.HTTPClient.GetFilesWithResponse(
		ctx,
		&envd.GetFilesParams{Path: &filePath, Username: sharedUtils.ToPtr("user")},
		setup.WithSandbox(sbx.JSON201.SandboxID),
	)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, getRes.StatusCode())
}

func TestDownloadFileWithoutSigningWhenAuthIsEnabled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	sbx := createSandbox(t, true, setup.WithAPIKey())
	assert.NotNil(t, sbx.JSON201)
	assert.NotNil(t, sbx.JSON201.EnvdAccessToken)

	envdClient := setup.GetEnvdClient(t, ctx)
	envdToken := sbx.JSON201.EnvdAccessToken

	// create test file
	filePath := "test.txt"
	textFile, contentType := utils.CreateTextFile(t, filePath, "Hello, World!")

	writeFileSigning := generateSignature(filePath, "user", "write", nil, *envdToken)
	writeRes, err := envdClient.HTTPClient.PostFilesWithBodyWithResponse(
		ctx,
		&envd.PostFilesParams{
			Path:      &filePath,
			Username:  sharedUtils.ToPtr("user"),
			Signature: &writeFileSigning,
		},
		contentType,
		textFile,
		setup.WithSandbox(sbx.JSON201.SandboxID),
		setup.WithEnvdAccessToken(*envdToken),
	)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, writeRes.StatusCode())

	readRes, readErr := envdClient.HTTPClient.GetFiles(
		ctx,
		&envd.GetFilesParams{Path: &filePath, Username: sharedUtils.ToPtr("user")},
		setup.WithSandbox(sbx.JSON201.SandboxID),
	)
	require.NoError(t, readErr)
	require.NoError(t, readRes.Body.Close())
	assert.Equal(t, http.StatusUnauthorized, readRes.StatusCode)
}

func TestDownloadFileWithSigningWhenAuthIsEnabled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	sbx := createSandbox(t, true, setup.WithAPIKey())
	assert.NotNil(t, sbx.JSON201)
	assert.NotNil(t, sbx.JSON201.EnvdAccessToken)

	envdClient := setup.GetEnvdClient(t, ctx)
	envdToken := sbx.JSON201.EnvdAccessToken

	// create test file
	filePath := "test.txt"
	readFileSigning := generateSignature(filePath, "user", "read", nil, *envdToken)
	writeFileSigning := generateSignature(filePath, "user", "write", nil, *envdToken)
	textFile, contentType := utils.CreateTextFile(t, filePath, "Hello, World!")

	writeRes, err := envdClient.HTTPClient.PostFilesWithBodyWithResponse(
		ctx,
		&envd.PostFilesParams{
			Path:      &filePath,
			Username:  sharedUtils.ToPtr("user"),
			Signature: &writeFileSigning,
		},
		contentType,
		textFile,
		setup.WithSandbox(sbx.JSON201.SandboxID),
	)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, writeRes.StatusCode())

	readRes, readErr := envdClient.HTTPClient.GetFilesWithResponse(
		ctx,
		&envd.GetFilesParams{Path: &filePath, Username: sharedUtils.ToPtr("user"), Signature: &readFileSigning},
		setup.WithSandbox(sbx.JSON201.SandboxID),
	)

	require.NoError(t, readErr)
	assert.Equal(t, http.StatusOK, readRes.StatusCode())
	assert.Equal(t, "Hello, World!", string(readRes.Body))
}

func TestDownloadWithAlreadyExpiredToken(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	sbx := createSandbox(t, true, setup.WithAPIKey())
	assert.NotNil(t, sbx.JSON201)
	assert.NotNil(t, sbx.JSON201.EnvdAccessToken)

	envdClient := setup.GetEnvdClient(t, ctx)
	envdToken := sbx.JSON201.EnvdAccessToken

	// create test file
	filePath := "demo/test.txt"
	signatureExpiration := time.Now().Add(-3 * time.Hour).Unix()
	signatureForRead := generateSignature(filePath, "user", "read", &signatureExpiration, *envdToken)

	readExpiration := int(signatureExpiration)
	readRes, readErr := envdClient.HTTPClient.GetFilesWithResponse(
		ctx,
		&envd.GetFilesParams{
			Path:                &filePath,
			Username:            sharedUtils.ToPtr("user"),
			Signature:           &signatureForRead,
			SignatureExpiration: &readExpiration,
		},
		setup.WithSandbox(sbx.JSON201.SandboxID),
	)

	require.NoError(t, readErr)
	assert.Equal(t, http.StatusUnauthorized, readRes.StatusCode())
	assert.JSONEq(t, "{\"code\":401,\"message\":\"signature is already expired\"}\n", string(readRes.Body))
}

func TestDownloadWithHealthyToken(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	sbx := createSandbox(t, true, setup.WithAPIKey())
	assert.NotNil(t, sbx.JSON201)
	assert.NotNil(t, sbx.JSON201.EnvdAccessToken)

	envdClient := setup.GetEnvdClient(t, ctx)
	envdToken := sbx.JSON201.EnvdAccessToken

	// create test file
	filePath := "demo/test.txt"
	signatureExpiration := time.Now().Add(1 * time.Minute).Unix()
	signatureForRead := generateSignature(filePath, "user", "read", &signatureExpiration, *envdToken)

	readExpiration := int(signatureExpiration)
	readRes, readErr := envdClient.HTTPClient.GetFilesWithResponse(
		ctx,
		&envd.GetFilesParams{
			Path:                &filePath,
			Username:            sharedUtils.ToPtr("user"),
			Signature:           &signatureForRead,
			SignatureExpiration: &readExpiration,
		},
		setup.WithSandbox(sbx.JSON201.SandboxID),
	)

	require.NoError(t, readErr)
	assert.Equal(t, http.StatusNotFound, readRes.StatusCode())
	assert.JSONEq(t, "{\"code\":404,\"message\":\"path '/home/user/demo/test.txt' does not exist\"}\n", string(readRes.Body))
}

func TestAccessWithNotCorrespondingSignatureAndSignatureExpiration(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	sbx := createSandbox(t, true, setup.WithAPIKey())
	assert.NotNil(t, sbx.JSON201)
	assert.NotNil(t, sbx.JSON201.EnvdAccessToken)

	envdClient := setup.GetEnvdClient(t, ctx)
	envdToken := sbx.JSON201.EnvdAccessToken

	// create test file
	filePath := "demo/test.txt"
	signatureExpiration := time.Now().Add(-1 * time.Minute).Unix()
	signatureForRead := generateSignature(filePath, "user", "read", nil, *envdToken)

	readExpiration := int(signatureExpiration)
	readRes, readErr := envdClient.HTTPClient.GetFilesWithResponse(
		ctx,
		&envd.GetFilesParams{
			Path:                &filePath,
			Username:            sharedUtils.ToPtr("user"),
			Signature:           &signatureForRead,
			SignatureExpiration: &readExpiration,
		},
		setup.WithSandbox(sbx.JSON201.SandboxID),
	)

	require.NoError(t, readErr)
	assert.Equal(t, http.StatusUnauthorized, readRes.StatusCode())
	assert.JSONEq(t, "{\"code\":401,\"message\":\"invalid signature\"}\n", string(readRes.Body))
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
