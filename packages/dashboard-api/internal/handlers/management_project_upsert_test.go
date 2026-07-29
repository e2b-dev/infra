package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

// The caller cannot tell a create from a reconcile, so one request serves both
// and the status carries the answer.
func TestUpsertProjectCreatesThenReconciles(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	store, cache := newUpsertStore(db)
	project := newProjectFixture()

	created := callUpsertProject(t, store, project.id, project.request())
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	require.Empty(t, cache.invalidated, "a project that did not exist has nothing cached")

	renamed := project.request()
	renamed.Name = "Acme Renamed"

	reconciled := callUpsertProject(t, store, project.id, renamed)
	require.Equal(t, http.StatusOK, reconciled.Code, reconciled.Body.String())

	require.Equal(t, "Acme Renamed", teamColumn(t, db, project.id, "name"))
	// The team changed, so every cached copy of it is stale.
	require.Equal(t, []uuid.UUID{project.id}, cache.invalidated)
}

// A retry after a response the caller never saw has to land on the same state.
func TestUpsertProjectIsIdempotent(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	store, _ := newUpsertStore(db)
	project := newProjectFixture()

	require.Equal(t, http.StatusCreated, callUpsertProject(t, store, project.id, project.request()).Code)

	repeated := callUpsertProject(t, store, project.id, project.request())
	require.Equal(t, http.StatusOK, repeated.Code, repeated.Body.String())
	require.Equal(t, "Acme", teamColumn(t, db, project.id, "name"))
}

// The slug is the project's DNS label, and the caller's region-wide namespace
// depends on it not moving.
func TestUpsertProjectRefusesAChangedSlug(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	store, _ := newUpsertStore(db)
	project := newProjectFixture()

	require.Equal(t, http.StatusCreated, callUpsertProject(t, store, project.id, project.request()).Code)

	moved := project.request()
	moved.Slug += "-moved"

	movedResponse := callUpsertProject(t, store, project.id, moved)
	require.Equal(t, http.StatusConflict, movedResponse.Code, movedResponse.Body.String())
	require.Equal(t, project.slug, teamColumn(t, db, project.id, "slug"))
}

// Slugs are unique cluster-wide, so the collision may be with a project the
// caller has never heard of.
func TestUpsertProjectRejectsASlugTakenByAnotherProject(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	store, _ := newUpsertStore(db)
	incumbent := newProjectFixture()

	require.Equal(t, http.StatusCreated, callUpsertProject(t, store, incumbent.id, incumbent.request()).Code)

	challenger := newProjectFixture()
	duplicate := challenger.request()
	duplicate.Slug = incumbent.slug

	collision := callUpsertProject(t, store, challenger.id, duplicate)
	require.Equal(t, http.StatusConflict, collision.Code, collision.Body.String())
}

// A routine name push must not undo an operator's decision, nor move a project
// off limits that arrived through their own route.
func TestUpsertProjectPreservesBlockedStateAndTier(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	store, _ := newUpsertStore(db)
	project := newProjectFixture()

	require.Equal(t, http.StatusCreated, callUpsertProject(t, store, project.id, project.request()).Code)

	// project_type names tiers this cluster has never heard of, which is why it
	// is not mapped onto teams.tier — doing so would fail this very constraint.
	require.NoError(t, db.AuthDB.TestsRawSQL(t.Context(),
		`INSERT INTO public.tiers (id, name, disk_mb, concurrent_instances, max_length_hours,
			max_vcpu, max_ram_mb, concurrent_template_builds, events_ttl_days,
			default_free_disk_size_mb, max_disk_size_mb)
		 VALUES ('pro_v1', 'Pro', 20480, 100, 24, 8, 8192, 20, 30, 10240, 51200)
		 ON CONFLICT (id) DO NOTHING`))
	require.NoError(t, db.AuthDB.TestsRawSQL(t.Context(),
		"UPDATE public.teams SET is_blocked = true, tier = 'pro_v1' WHERE id = $1", project.id))

	reconcile := project.request()
	reconcile.Name = "Acme Renamed"
	reconcile.ProjectType = "enterprise_v3"

	renamed := callUpsertProject(t, store, project.id, reconcile)
	require.Equal(t, http.StatusOK, renamed.Code, renamed.Body.String())

	require.Equal(t, "true", teamColumn(t, db, project.id, "is_blocked::text"))
	require.Equal(t, "pro_v1", teamColumn(t, db, project.id, "tier"))
}

// Required to create, optional to reconcile — the asymmetry the contract cannot
// express on one operation.
func TestUpsertProjectRequiresAnEmailOnlyToCreate(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	store, _ := newUpsertStore(db)
	project := newProjectFixture()
	withoutEmail := project.request()
	withoutEmail.Email = nil

	refused := callUpsertProject(t, store, project.id, withoutEmail)
	require.Equal(t, http.StatusBadRequest, refused.Code, refused.Body.String())

	require.Equal(t, http.StatusCreated, callUpsertProject(t, store, project.id, project.request()).Code)

	// The project now has an address, so a caller that stopped sending one is
	// omitting a value rather than clearing it.
	require.Equal(t, http.StatusOK, callUpsertProject(t, store, project.id, withoutEmail).Code)
	require.Equal(t, "ops@acme.test", teamColumn(t, db, project.id, "email"))
}

func TestUpsertProjectUpdatesTheEmailWhenOneIsSent(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	store, _ := newUpsertStore(db)
	project := newProjectFixture()

	require.Equal(t, http.StatusCreated, callUpsertProject(t, store, project.id, project.request()).Code)
	rebilled := project.request()
	rebilled.Email = new("billing@acme.test")

	require.Equal(t, http.StatusOK, callUpsertProject(t, store, project.id, rebilled).Code)

	require.Equal(t, "billing@acme.test", teamColumn(t, db, project.id, "email"))
}

func TestUpsertProjectEchoesTheStoredProject(t *testing.T) {
	t.Parallel()

	db := testutils.SetupDatabase(t)
	store, _ := newUpsertStore(db)
	project := newProjectFixture()

	pro := project.request()
	pro.ProjectType = "pro_v1"

	recorder := callUpsertProject(t, store, project.id, pro)
	require.Equal(t, http.StatusCreated, recorder.Code)

	var decoded api.ManagementProject
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &decoded))
	require.Equal(t, project.id, decoded.Id)
	require.Equal(t, "Acme", decoded.Name)
	require.Equal(t, project.slug, decoded.Slug)
	// Echoed from the request: there is no column for it.
	require.Equal(t, "pro_v1", decoded.ProjectType)
	require.NotNil(t, decoded.Email)
	require.Equal(t, "ops@acme.test", *decoded.Email)
}

// Deleting a project needs teardown this process cannot reach. If you are
// removing this test, read the handler comment first.
func TestDeleteProjectIsDeliberatelyNotImplemented(t *testing.T) {
	t.Parallel()

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodDelete,
		"/v1/management/projects/"+uuid.NewString(), nil)

	(&APIStore{}).ManagementDeleteProject(ginCtx, uuid.New())

	require.Equal(t, http.StatusNotImplemented, recorder.Code)
}

// upsertFixture is a valid project with a slug unique to its test, so cases
// that care about one field can change that one and send the rest.
type projectFixture struct {
	id   uuid.UUID
	slug string
}

func newProjectFixture() projectFixture {
	id := uuid.New()

	return projectFixture{id: id, slug: "acme-" + id.String()[:8]}
}

func (p projectFixture) request() api.ManagementProjectUpsertRequest {
	email := "ops@acme.test"

	return api.ManagementProjectUpsertRequest{
		Name:        "Acme",
		Slug:        p.slug,
		ProjectType: "base_v1",
		Email:       &email,
	}
}

func newUpsertStore(db *testutils.Database) (*APIStore, *recordingCacheAuthService) {
	auth := &recordingCacheAuthService{}

	return &APIStore{authDB: db.AuthDB, authService: auth}, auth
}

func callUpsertProject(t *testing.T, store *APIStore, projectID uuid.UUID, request api.ManagementProjectUpsertRequest) *httptest.ResponseRecorder {
	t.Helper()

	body, err := json.Marshal(request)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequestWithContext(t.Context(), http.MethodPut,
		"/v1/management/projects/"+projectID.String(), bytes.NewReader(body))
	ginCtx.Request.Header.Set("Content-Type", "application/json")

	store.ManagementUpsertProject(ginCtx, projectID)
	ginCtx.Writer.WriteHeaderNow()

	return recorder
}

func teamColumn(t *testing.T, db *testutils.Database, teamID uuid.UUID, column string) string {
	t.Helper()

	var value string
	err := db.AuthDB.TestsRawSQLQuery(t.Context(),
		"SELECT "+column+" FROM public.teams WHERE id = $1",
		func(rows pgx.Rows) error {
			rows.Next()

			return rows.Scan(&value)
		}, teamID)
	require.NoError(t, err)

	return value
}
