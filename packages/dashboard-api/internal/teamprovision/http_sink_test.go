package teamprovision

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

	sink := NewHTTPProvisionSink(server.URL, "token")
	err := sink.ProvisionTeam(t.Context(), testProvisionRequest())
	require.Error(t, err)

	var provisionErr *ProvisionError
	require.ErrorAs(t, err, &provisionErr)
	require.Equal(t, http.StatusBadRequest, provisionErr.StatusCode)
	require.Equal(t, "invalid payload", provisionErr.Message)
	require.EqualValues(t, 1, requestCount.Load())
}

func TestHTTPProvisionSink_FallsBackToPlainTextResponse(t *testing.T) {
	t.Parallel()

	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream gateway exploded"))
	}))
	defer server.Close()

	sink := NewHTTPProvisionSink(server.URL, "token")
	sink.client.RetryWaitMin = time.Millisecond
	sink.client.RetryWaitMax = time.Millisecond
	err := sink.ProvisionTeam(t.Context(), testProvisionRequest())
	require.Error(t, err)

	var provisionErr *ProvisionError
	require.ErrorAs(t, err, &provisionErr)
	require.Equal(t, http.StatusBadGateway, provisionErr.StatusCode)
	require.Equal(t, "upstream gateway exploded", provisionErr.Message)
	require.EqualValues(t, sink.client.RetryMax+1, requestCount.Load())
}

func TestHTTPProvisionSink_FallsBackToStatusTextForEmptyBody(t *testing.T) {
	t.Parallel()

	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	sink := NewHTTPProvisionSink(server.URL, "token")
	sink.client.RetryWaitMin = time.Millisecond
	sink.client.RetryWaitMax = time.Millisecond
	err := sink.ProvisionTeam(t.Context(), testProvisionRequest())
	require.Error(t, err)

	var provisionErr *ProvisionError
	require.ErrorAs(t, err, &provisionErr)
	require.Equal(t, http.StatusServiceUnavailable, provisionErr.StatusCode)
	require.Equal(t, http.StatusText(http.StatusServiceUnavailable), provisionErr.Message)
	require.EqualValues(t, sink.client.RetryMax+1, requestCount.Load())
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

	sink := NewHTTPProvisionSink(server.URL, "token")
	sink.client.RetryWaitMin = time.Millisecond
	sink.client.RetryWaitMax = time.Millisecond
	err := sink.ProvisionTeam(t.Context(), testProvisionRequest())
	require.NoError(t, err)
	require.EqualValues(t, 2, requestCount.Load())
}

func TestHTTPProvisionSink_RetriesTransportErrorsAndSucceeds(t *testing.T) {
	t.Parallel()

	sink := NewHTTPProvisionSink("http://billing.example", "token")
	sink.client.RetryWaitMin = time.Millisecond
	sink.client.RetryWaitMax = time.Millisecond

	var attemptCount atomic.Int32
	sink.client.HTTPClient.Transport = roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		attempt := attemptCount.Add(1)
		if attempt == 1 {
			return nil, errors.New("temporary dial failure")
		}

		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("")),
			Header:     make(http.Header),
		}, nil
	})

	err := sink.ProvisionTeam(t.Context(), testProvisionRequest())
	require.NoError(t, err)
	require.EqualValues(t, 2, attemptCount.Load())
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

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
