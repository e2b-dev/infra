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

	var decoded api.ManagementMemberBatchRequest
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decoding the payload callers send: %v", err)
	}

	want := api.ManagementMemberBatchRequest{
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

// project_type is gone from the contract. It named the caller's plan
// vocabulary, which this side never had a column for or an opinion about: the
// tier is assigned once at creation from a local default, and the limits that
// actually matter arrive absolute through upsertProjectLimits.
//
// Removing it is safe to ship ahead of the callers. Nothing declares
// additionalProperties: false, so a caller still sending the field has it
// ignored rather than rejected — the break is at their next codegen, not at
// runtime.
func TestProjectUpsertIgnoresARetiredProjectType(t *testing.T) {
	t.Parallel()

	body := `{"name":"Acme","slug":"acme","email":"ops@acme.test","project_type":"enterprise_v3"}`

	var decoded api.ManagementProjectUpsertRequest
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decoding an upsert that still carries project_type: %v", err)
	}

	want := api.ManagementProjectUpsertRequest{Name: "Acme", Slug: "acme", Email: "ops@acme.test"}
	if decoded != want {
		t.Errorf("decoded %+v, want %+v", decoded, want)
	}
}

// Every declared operation has to reach its own handler. Only another
// repository's generated client exercises this surface, so a route registered
// against the wrong path fails first in an integration nobody runs here.
//
// The batch route is why this is a table: it sits beside /members/{userId},
// exactly where a router reads "batch" as a user id and dispatches wrongly.
func TestEveryManagementRouteReachesItsHandler(t *testing.T) {
	t.Parallel()

	teamID, userID := uuid.New().String(), uuid.New().String()
	project := "/v1/management/projects/" + teamID

	for _, tt := range []struct {
		operation string
		method    string
		path      string
		body      string
	}{
		{"upsertProject", http.MethodPut, project, `{"name":"a","slug":"a","project_type":"base_v1"}`},
		{"deleteProject", http.MethodDelete, project, ""},
		{"upsertMember", http.MethodPut, project + "/members/" + userID, `{}`},
		{"deleteMember", http.MethodDelete, project + "/members/" + userID, ""},
		{"batchMembers", http.MethodPost, project + "/members/batch", `[]`},
		{"upsertLimits", http.MethodPut, project + "/limits", `{}`},
		{"purgeUser", http.MethodDelete, "/v1/management/users/" + userID, ""},
	} {
		t.Run(tt.operation, func(t *testing.T) {
			t.Parallel()

			reached := make(chan string, 1)
			router := gin.New()
			api.RegisterHandlers(router, &routeRecorder{reached: reached})

			recorder := httptest.NewRecorder()
			request := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, strings.NewReader(tt.body))
			request.Header.Set("Content-Type", "application/json")

			router.ServeHTTP(recorder, request)

			select {
			case got := <-reached:
				if got != tt.operation {
					t.Fatalf("request reached %q, want %q", got, tt.operation)
				}
			default:
				t.Fatalf("no handler was reached; status %d", recorder.Code)
			}
		})
	}
}

// routeRecorder reports which operation ran. Embedding the generated interface
// leaves the rest nil: reaching one panics, which is the failure under test.
type routeRecorder struct {
	api.ServerInterface

	reached chan string
}

func (r *routeRecorder) report(c *gin.Context, operation string) {
	r.reached <- operation
	c.Status(http.StatusNoContent)
}

func (r *routeRecorder) ManagementUpsertProject(c *gin.Context, _ api.TeamID) {
	r.report(c, "upsertProject")
}

func (r *routeRecorder) ManagementDeleteProject(c *gin.Context, _ api.TeamID) {
	r.report(c, "deleteProject")
}

func (r *routeRecorder) ManagementUpsertProjectMember(c *gin.Context, _ api.TeamID, _ api.UserId) {
	r.report(c, "upsertMember")
}

func (r *routeRecorder) ManagementDeleteProjectMember(c *gin.Context, _ api.TeamID, _ api.UserId) {
	r.report(c, "deleteMember")
}

func (r *routeRecorder) ManagementBatchSyncProjectMembers(c *gin.Context, _ api.TeamID) {
	r.report(c, "batchMembers")
}

func (r *routeRecorder) ManagementUpsertProjectLimits(c *gin.Context, _ api.TeamID) {
	r.report(c, "upsertLimits")
}

func (r *routeRecorder) ManagementPurgeUser(c *gin.Context, _ api.UserId) {
	r.report(c, "purgeUser")
}
