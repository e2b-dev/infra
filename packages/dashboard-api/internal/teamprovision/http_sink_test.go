package teamprovision

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	sharedteamprovision "github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

func TestHTTPProvisionSink_ReturnsJSONErrorMessage(t *testing.T) {
	t.Parallel()

	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"invalid payload"}`))
	}))
	defer server.Close()

	sink := NewHTTPProvisionSink(server.URL, "token", nil)
	err := sink.ProvisionTeam(t.Context(), testProvisionRequest())
	require.Error(t, err)

	var provisionErr *ProvisionError
	require.ErrorAs(t, err, &provisionErr)
	require.Equal(t, http.StatusBadRequest, provisionErr.StatusCode)
	require.Equal(t, "invalid payload", provisionErr.Message)
	require.EqualValues(t, 1, requestCount.Load())
}

func TestHTTPProvisionSink_RetriesRetryableResponsesAndSucceeds(t *testing.T) {
	t.Parallel()

	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempt := requestCount.Add(1)
		if attempt == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("temporary outage"))

			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink := NewHTTPProvisionSink(server.URL, "token", nil)
	sink.client.RetryWaitMin = time.Millisecond
	sink.client.RetryWaitMax = time.Millisecond
	err := sink.ProvisionTeam(t.Context(), testProvisionRequest())
	require.NoError(t, err)
	require.EqualValues(t, 2, requestCount.Load())
}

func TestHTTPProvisionSink_RetriesRequestTimeoutWithinOverallBudget(t *testing.T) {
	t.Parallel()

	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempt := requestCount.Add(1)
		if attempt == 1 {
			time.Sleep(40 * time.Millisecond)
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sink := NewHTTPProvisionSink(server.URL, "token", nil)
	sink.timeout = 80 * time.Millisecond
	sink.client.HTTPClient.Timeout = 25 * time.Millisecond
	sink.client.RetryWaitMin = time.Millisecond
	sink.client.RetryWaitMax = time.Millisecond

	err := sink.ProvisionTeam(t.Context(), testProvisionRequest())
	require.NoError(t, err)
	require.EqualValues(t, 2, requestCount.Load())
}

func testProvisionRequest() sharedteamprovision.TeamBillingProvisionRequestedV1 {
	return sharedteamprovision.TeamBillingProvisionRequestedV1{
		TeamID:        uuid.New(),
		TeamName:      "Acme",
		TeamEmail:     "acme@example.com",
		CreatorUserID: uuid.New(),
		Reason:        sharedteamprovision.ReasonAdditionalTeam,
	}
}
