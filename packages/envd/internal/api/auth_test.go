package api

import (
	"fmt"
	"github.com/e2b-dev/infra/packages/shared/pkg/keys"
	"github.com/stretchr/testify/assert"
	"strconv"
	"testing"
	"time"
)

func TestKeyGenerationAlgorithmIsStable(t *testing.T) {
	apiToken := "secret-access-token"
	api := &API{accessToken: &apiToken}

	path := "/path/to/demo.txt"
	username := "root"
	operation := "write"
	timestamp := time.Now().Unix()

	signature, err := api.generateSignature(path, username, operation, &timestamp)
	assert.NoError(t, err)
	assert.NotEmpty(t, signature)

	// locally generated signature
	hasher := keys.NewSHA256Hashing()
	localSignatureTmp := fmt.Sprintf("%s:%s:%s:%s:%s", path, operation, username, apiToken, strconv.FormatInt(timestamp, 10))
	localSignature := fmt.Sprintf("v1_%s", hasher.HashWithoutPrefix([]byte(localSignatureTmp)))

	assert.Equal(t, localSignature, signature)
}

func TestKeyGenerationAlgorithmWithoutExpirationIsStable(t *testing.T) {
	apiToken := "secret-access-token"
	api := &API{accessToken: &apiToken}

	path := "/path/to/resource.txt"
	username := "user"
	operation := "read"

	signature, err := api.generateSignature(path, username, operation, nil)
	assert.NoError(t, err)
	assert.NotEmpty(t, signature)

	// locally generated signature
	hasher := keys.NewSHA256Hashing()
	localSignatureTmp := fmt.Sprintf("%s:%s:%s:%s", path, operation, username, apiToken)
	localSignature := fmt.Sprintf("v1_%s", hasher.HashWithoutPrefix([]byte(localSignatureTmp)))

	assert.Equal(t, localSignature, signature)
}
