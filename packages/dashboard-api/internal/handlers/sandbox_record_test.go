package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	authtypes "github.com/e2b-dev/infra/packages/auth/pkg/types"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/queries"
)

type noRowsDB struct{}

func (d noRowsDB) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (d noRowsDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, nil
}

func (d noRowsDB) QueryRow(context.Context, string, ...any) pgx.Row {
	return noRowsRow{}
}

type noRowsRow struct{}

func (r noRowsRow) Scan(...any) error {
	return pgx.ErrNoRows
}

func TestGetSandboxesSandboxIDRecordReturns404WhenRecordRetentionNotMet(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/sandboxes/sbx_1/record", nil)

	teamID := uuid.New()
	auth.SetTeamInfo(ctx, &authtypes.Team{
		Team: &authqueries.Team{
			ID: teamID,
		},
	})

	store := &APIStore{
		db: &sqlcdb.Client{
			Queries: queries.New(noRowsDB{}),
		},
	}

	store.GetSandboxesSandboxIDRecord(ctx, api.SandboxID("sbx_1"))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, recorder.Code)
	}

	var response struct {
		Code    int32  `json:"code"`
		Message string `json:"message"`
	}

	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response body: %v", err)
	}

	if response.Code != int32(http.StatusNotFound) {
		t.Fatalf("expected code %d, got %d", http.StatusNotFound, response.Code)
	}
}
