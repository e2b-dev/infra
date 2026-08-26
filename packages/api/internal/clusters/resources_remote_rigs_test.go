package clusters

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	edgeapi "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
)

// rigEdge stands in for a cluster's edge deployment, recording what the
// passthrough sent.
type rigEdge struct {
	server *httptest.Server

	method string
	path   string
	query  string
	body   string

	status int
	reply  string
}

func newRigEdge(t *testing.T, status int, reply string) *rigEdge {
	t.Helper()

	e := &rigEdge{status: status, reply: reply}
	e.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.method = r.Method
		e.path = r.URL.Path
		e.query = r.URL.RawQuery

		body, _ := io.ReadAll(r.Body)
		e.body = string(body)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(e.status)
		_, _ = w.Write([]byte(e.reply))
	}))
	t.Cleanup(e.server.Close)

	return e
}

func (e *rigEdge) provider(t *testing.T) *ClusterResourceProviderImpl {
	t.Helper()

	client, err := edgeapi.NewClientWithResponses(e.server.URL)
	require.NoError(t, err)

	return &ClusterResourceProviderImpl{
		clusterID: uuid.New(),
		instances: smap.New[*Instance](),
		client:    client,
	}
}

func TestGetRigsPassesThroughEdgeRigs(t *testing.T) {
	t.Parallel()

	edge := newRigEdge(t, http.StatusOK, `{"items":[
		{"id":"default","provider":"aws","resourceId":"arn:aws:autoscaling:us-west-2:1:autoScalingGroup:uuid:autoScalingGroupName/g","capacityDesired":2,"capacityCurrent":2,"capacityMin":1,"capacityMax":4},
		{"id":"big","provider":"gcp","resourceId":"https://www.googleapis.com/compute/v1/projects/p/regions/r/instanceGroupManagers/m","capacityDesired":1,"capacityCurrent":0}
	]}`)

	rigs, apiErr := edge.provider(t).GetRigs(t.Context())
	require.Nil(t, apiErr)
	require.Len(t, rigs, 2)

	assert.Equal(t, http.MethodGet, edge.method)
	assert.Equal(t, "/v1/rigs", edge.path)

	assert.Equal(t, "default", rigs[0].Id)
	assert.Equal(t, "aws", rigs[0].Provider)
	assert.Equal(t, int32(2), rigs[0].CapacityDesired)
	require.NotNil(t, rigs[0].CapacityMin)
	assert.Equal(t, int32(1), *rigs[0].CapacityMin)
	require.NotNil(t, rigs[0].CapacityMax)
	assert.Equal(t, int32(4), *rigs[0].CapacityMax)

	// A GCP rig without an enforcing autoscaler reports no bounds; the
	// passthrough must keep them absent rather than inventing zeros.
	assert.Equal(t, "gcp", rigs[1].Provider)
	assert.Nil(t, rigs[1].CapacityMin)
	assert.Nil(t, rigs[1].CapacityMax)
}

// A deployment without rig management answers with an empty array, not an error.
func TestGetRigsAcceptsEmptyList(t *testing.T) {
	t.Parallel()

	rigs, apiErr := newRigEdge(t, http.StatusOK, `{"items":[]}`).provider(t).GetRigs(t.Context())
	require.Nil(t, apiErr)
	assert.Empty(t, rigs)
}

func TestSetRigCapacitySendsDesiredAndAcceptsAccepted(t *testing.T) {
	t.Parallel()

	edge := newRigEdge(t, http.StatusAccepted, "")

	apiErr := edge.provider(t).SetRigCapacity(t.Context(), "default", 3)
	require.Nil(t, apiErr)

	assert.Equal(t, http.MethodPut, edge.method)
	assert.Equal(t, "/v1/rigs/default/capacity", edge.path)

	var sent map[string]any
	require.NoError(t, json.Unmarshal([]byte(edge.body), &sent))
	assert.InDelta(t, float64(3), sent["desired"], 0)
}

func TestTerminateRigInstanceForwardsDecrementDesired(t *testing.T) {
	t.Parallel()

	for _, decrement := range []bool{true, false} {
		edge := newRigEdge(t, http.StatusAccepted, "")

		apiErr := edge.provider(t).TerminateRigInstance(t.Context(), "i-123", decrement)
		require.Nil(t, apiErr)

		assert.Equal(t, http.MethodDelete, edge.method)
		assert.Equal(t, "/v1/rigs/instances/i-123", edge.path)

		if decrement {
			assert.Equal(t, "decrementDesired=true", edge.query)
		} else {
			assert.Equal(t, "decrementDesired=false", edge.query)
		}
	}
}

func TestGetRigInstancesPassesThroughAgeAndTransitionState(t *testing.T) {
	t.Parallel()

	edge := newRigEdge(t, http.StatusOK, `{"items":[
		{"id":"i-1","createdAt":"2026-08-24T12:06:44Z","transitioning":false,"terminating":false},
		{"id":"i-2","createdAt":null,"transitioning":true,"terminating":true}
	]}`)

	instances, apiErr := edge.provider(t).GetRigInstances(t.Context(), "default")
	require.Nil(t, apiErr)
	require.Len(t, instances, 2)

	assert.Equal(t, "/v1/rigs/default/instances", edge.path)

	require.NotNil(t, instances[0].CreatedAt)
	assert.Equal(t, time.Date(2026, 8, 24, 12, 6, 44, 0, time.UTC), instances[0].CreatedAt.UTC())
	assert.False(t, instances[0].Transitioning)

	// An instance the provider is still creating has no creation time yet.
	assert.Nil(t, instances[1].CreatedAt)
	assert.True(t, instances[1].Transitioning)
	assert.True(t, instances[1].Terminating)
}

func TestGetRigErrorsForwardsLimitAndOptionalFields(t *testing.T) {
	t.Parallel()

	edge := newRigEdge(t, http.StatusOK, `[
		{"timestamp":"2026-08-24T12:00:00Z","code":"Failed","message":"boom","instance":"i-1","action":"CREATING"},
		{"timestamp":"2026-08-24T11:00:00Z","code":"Cancelled","message":"gone"}
	]`)

	limit := int32(5)
	rigErrors, apiErr := edge.provider(t).GetRigErrors(t.Context(), "default", &limit)
	require.Nil(t, apiErr)
	require.Len(t, rigErrors, 2)

	assert.Equal(t, "/v1/rigs/default/errors", edge.path)
	assert.Equal(t, "limit=5", edge.query)

	require.NotNil(t, rigErrors[0].Instance)
	assert.Equal(t, "i-1", *rigErrors[0].Instance)
	assert.Nil(t, rigErrors[1].Instance)
	assert.Nil(t, rigErrors[1].Action)
}

func TestRigErrorStatusesAreForwarded(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		edgeStatus int
		edgeBody   string
		wantStatus int
		wantMsg    string
	}{
		{
			name:       "capacity outside the group bounds",
			edgeStatus: http.StatusBadRequest,
			edgeBody:   `{"code":400,"message":"Desired capacity:9 must be between the specified min size:1 and max size:4"}`,
			wantStatus: http.StatusBadRequest,
			wantMsg:    "Desired capacity:9 must be between the specified min size:1 and max size:4",
		},
		{
			name:       "unknown rig",
			edgeStatus: http.StatusNotFound,
			edgeBody:   `{"code":404,"message":"rig not found"}`,
			wantStatus: http.StatusNotFound,
			wantMsg:    "rig not found",
		},
		{
			name:       "concurrent change",
			edgeStatus: http.StatusConflict,
			edgeBody:   `{"code":409,"message":"scaling activity in progress"}`,
			wantStatus: http.StatusConflict,
			wantMsg:    "scaling activity in progress",
		},
		{
			name:       "deployment without rig management",
			edgeStatus: http.StatusNotImplemented,
			edgeBody:   `{"code":501,"message":"rig management is not supported by this deployment configuration"}`,
			wantStatus: http.StatusNotImplemented,
			wantMsg:    "rig management is not supported by this deployment configuration",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			apiErr := newRigEdge(t, tc.edgeStatus, tc.edgeBody).provider(t).SetRigCapacity(t.Context(), "default", 9)
			require.NotNil(t, apiErr)
			assert.Equal(t, tc.wantStatus, apiErr.Code)
			assert.Equal(t, tc.wantMsg, apiErr.ClientMsg)
		})
	}
}

func TestEdgeUnauthorizedBecomesInternalError(t *testing.T) {
	t.Parallel()

	apiErr := newRigEdge(t, http.StatusUnauthorized, `{"code":401,"message":"invalid api key"}`).
		provider(t).SetRigCapacity(t.Context(), "default", 3)

	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
	assert.Equal(t, "Failed to set rig capacity", apiErr.ClientMsg)
	assert.Contains(t, apiErr.Err.Error(), "invalid api key")
}

func TestLocalClusterReportsRigsUnsupported(t *testing.T) {
	t.Parallel()

	local := &LocalClusterResourceProvider{}

	_, apiErr := local.GetRigs(t.Context())
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusNotImplemented, apiErr.Code)

	assert.Equal(t, http.StatusNotImplemented, local.SetRigCapacity(t.Context(), "default", 1).Code)
	assert.Equal(t, http.StatusNotImplemented, local.TerminateRigInstance(t.Context(), "i-1", true).Code)

	_, apiErr = local.GetRigInstances(t.Context(), "default")
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusNotImplemented, apiErr.Code)

	_, apiErr = local.GetRigErrors(t.Context(), "default", nil)
	require.NotNil(t, apiErr)
	assert.Equal(t, http.StatusNotImplemented, apiErr.Code)
}
