package teamprovision

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	sharedteamprovision "github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

func TestHTTPProvisionSink_ReturnsJSONErrorMessage(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"invalid payload"}`))
	}))
	defer server.Close()

	sink := NewHTTPProvisionSink(server.URL, "token", 0)
	err := sink.ProvisionTeam(t.Context(), testProvisionRequest())
	require.Error(t, err)

	var provisionErr *ProvisionError
	require.ErrorAs(t, err, &provisionErr)
	require.Equal(t, http.StatusBadRequest, provisionErr.StatusCode)
	require.Equal(t, "invalid payload", provisionErr.Message)
}

func TestHTTPProvisionSink_FallsBackToPlainTextResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream gateway exploded"))
	}))
	defer server.Close()

	sink := NewHTTPProvisionSink(server.URL, "token", 0)
	err := sink.ProvisionTeam(t.Context(), testProvisionRequest())
	require.Error(t, err)

	var provisionErr *ProvisionError
	require.ErrorAs(t, err, &provisionErr)
	require.Equal(t, http.StatusBadGateway, provisionErr.StatusCode)
	require.Equal(t, "upstream gateway exploded", provisionErr.Message)
}

func TestHTTPProvisionSink_FallsBackToStatusTextForEmptyBody(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	sink := NewHTTPProvisionSink(server.URL, "token", 0)
	err := sink.ProvisionTeam(t.Context(), testProvisionRequest())
	require.Error(t, err)

	var provisionErr *ProvisionError
	require.ErrorAs(t, err, &provisionErr)
	require.Equal(t, http.StatusServiceUnavailable, provisionErr.StatusCode)
	require.Equal(t, http.StatusText(http.StatusServiceUnavailable), provisionErr.Message)
}

func testProvisionRequest() sharedteamprovision.TeamBillingProvisionRequestedV1 {
	return sharedteamprovision.TeamBillingProvisionRequestedV1{
		TeamID:      uuid.New(),
		TeamName:    "Acme",
		TeamEmail:   "acme@example.com",
		OwnerUserID: uuid.New(),
		Reason:      sharedteamprovision.ReasonAdditionalTeam,
	}
}
