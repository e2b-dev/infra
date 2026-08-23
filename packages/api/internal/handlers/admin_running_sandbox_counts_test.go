package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

type fakeTeamRunningSandboxCounter struct {
	counts map[uuid.UUID]int64
	err    error
}

func (f fakeTeamRunningSandboxCounter) TeamRunningSandboxCounts(context.Context) (map[uuid.UUID]int64, error) {
	return f.counts, f.err
}

func TestGetAdminSandboxesRunningCounts(t *testing.T) {
	t.Parallel()

	firstTeamID := uuid.New()
	secondTeamID := uuid.New()
	store := &APIStore{
		teamSandboxCounter: fakeTeamRunningSandboxCounter{
			counts: map[uuid.UUID]int64{
				firstTeamID:  7,
				secondTeamID: 2,
			},
		},
	}
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/admin/sandboxes/running-counts",
		nil,
	)

	store.GetAdminSandboxesRunningCounts(ginContext)

	require.Equal(t, http.StatusOK, recorder.Code)

	var body api.AdminTeamRunningSandboxCounts
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	assert.Equal(t, api.AdminTeamRunningSandboxCounts{
		firstTeamID.String():  7,
		secondTeamID.String(): 2,
	}, body)
}

func TestGetAdminSandboxesRunningCountsReturnsStorageFailure(t *testing.T) {
	t.Parallel()

	store := &APIStore{
		teamSandboxCounter: fakeTeamRunningSandboxCounter{
			err: errors.New("redis unavailable"),
		},
	}
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/admin/sandboxes/running-counts",
		nil,
	)

	store.GetAdminSandboxesRunningCounts(ginContext)

	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
}
