package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
)

// The batch caller in belt's workspace-api is hand-written rather than
// generated from this spec, because it predates the route existing. Nothing
// therefore keeps the two in step: a rename here still compiles on both sides
// and fails only against a live cluster, as a 400 or a silently ignored field.
//
// This pins the wire shape that caller sends — the literal below is the JSON
// it produces — against the type generated from the spec.
func TestBatchMemberRequestMatchesTheShapeCallersSend(t *testing.T) {
	t.Parallel()

	present := uuid.New()
	absent := uuid.New()

	body := `[{"user_id":"` + present.String() + `","present":true},` +
		`{"user_id":"` + absent.String() + `","present":false}]`

	var decoded api.AdminControlPlaneMemberBatchRequest
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decoding the payload callers send: %v", err)
	}

	want := api.AdminControlPlaneMemberBatchRequest{
		{UserId: present, Present: true},
		{UserId: absent, Present: false},
	}

	if len(decoded) != len(want) {
		t.Fatalf("decoded %d entries, want %d", len(decoded), len(want))
	}
	for i, entry := range decoded {
		if entry != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, entry, want[i])
		}
	}
}

// project_type carried an enum of deployment environments from the scaffolding
// that predated any caller. The caller that arrived sends tier names, so every
// upsert failed validation client-side, before a request was ever made.
//
// Nothing on this side reads the value — there is no column for it, and limits
// arrive in full through upsertProjectLimits — so the contract has no business
// enumerating it. This pins that: a tier name decodes, which it cannot do if
// someone reintroduces a closed set that guesses at the caller's vocabulary.
func TestProjectUpsertAcceptsTheCallersOwnTierNames(t *testing.T) {
	t.Parallel()

	for _, projectType := range []string{"base_v1", "pro_v1", "enterprise_v3"} {
		body := `{"name":"Acme","slug":"acme","project_type":"` + projectType + `"}`

		var decoded api.AdminControlPlaneProjectUpsertRequest
		if err := json.Unmarshal([]byte(body), &decoded); err != nil {
			t.Fatalf("decoding an upsert with project_type %q: %v", projectType, err)
		}

		if decoded.ProjectType != projectType {
			t.Errorf("ProjectType = %q, want %q", decoded.ProjectType, projectType)
		}
	}
}

// A batch route sitting beside /members/{userId} is the arrangement where a
// router can read "batch" as a user id and hand the request to the wrong
// operation. Registering it and driving a request through proves which handler
// the path reaches.
func TestBatchMemberRouteIsNotShadowedByTheMemberParameter(t *testing.T) {
	t.Parallel()

	reached := make(chan string, 1)
	router := gin.New()
	api.RegisterHandlers(router, &routeRecorder{reached: reached})

	teamID := uuid.New()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/v1/management/projects/"+teamID.String()+"/members/batch",
		strings.NewReader(`[{"user_id":"`+uuid.New().String()+`","present":true}]`))
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	select {
	case got := <-reached:
		if got != "batch" {
			t.Fatalf("request reached %q, want the batch handler", got)
		}
	default:
		t.Fatalf("no handler was reached; status %d", recorder.Code)
	}
}

// routeRecorder answers the two member operations and reports which one ran.
// Embedding the generated interface leaves every other operation nil, which is
// fine: reaching one would panic, and that is the failure this test is for.
type routeRecorder struct {
	api.ServerInterface

	reached chan string
}

func (r *routeRecorder) BatchSyncProjectMembers(c *gin.Context, _ api.TeamID) {
	r.reached <- "batch"
	c.Status(http.StatusNoContent)
}

func (r *routeRecorder) UpsertProjectMember(c *gin.Context, _ api.TeamID, _ api.UserId) {
	r.reached <- "single"
	c.Status(http.StatusNoContent)
}
