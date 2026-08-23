package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/shared/pkg/apierrors"
)

func TestSendAPIError_BodyCarriesSemanticErrorCode(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)

	apierrors.SendAPIError(ginCtx, &api.APIError{
		Code:      http.StatusServiceUnavailable,
		ErrorCode: "sandbox_capacity_unavailable",
		ClientMsg: "Failed to place sandbox: not enough capacity for the requested resources right now, please retry shortly",
		Err:       errors.New("no nodes available"),
	})

	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)

	var body api.Error
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	assert.Equal(t, int32(http.StatusServiceUnavailable), body.Code)
	require.NotNil(t, body.ErrorCode)
	assert.Equal(t, "sandbox_capacity_unavailable", *body.ErrorCode)
	assert.Equal(t, "Failed to place sandbox: not enough capacity for the requested resources right now, please retry shortly", body.Message)
}

func TestSendAPIStoreError_BodyOmitsErrorCode(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)

	a := &APIStore{}
	a.sendAPIStoreError(ginCtx, http.StatusInternalServerError, "Failed to place sandbox")

	var body map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	assert.NotContains(t, body, "error_code")
}
