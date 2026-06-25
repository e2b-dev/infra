package handlers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

// channelProvisionSink records provisioning calls and can block until released,
// so tests can prove the call happens off the request path.
type channelProvisionSink struct {
	called chan teamprovision.TeamBillingProvisionRequestedV1
	block  chan struct{}
	err    error
}

func (s *channelProvisionSink) ProvisionTeam(_ context.Context, req teamprovision.TeamBillingProvisionRequestedV1) error {
	if s.block != nil {
		<-s.block
	}
	s.called <- req

	return s.err
}

func newTestProfile() bootstrapUserProfile {
	return bootstrapUserProfile{
		UserID:          uuid.New(),
		Email:           "user@example.com",
		DefaultTeamName: "User's Default Team",
		CreatorContext: &teamprovision.CreatorContextV1{
			IPAddress:  "1.2.3.4",
			UserAgent:  "test-agent",
			AuthMethod: teamprovision.AuthMethodSocial,
		},
	}
}

func newTestProvisionRequest() teamprovision.TeamBillingProvisionRequestedV1 {
	return teamprovision.TeamBillingProvisionRequestedV1{
		TeamID:        uuid.New(),
		TeamName:      "User's Default Team",
		TeamEmail:     "user@example.com",
		CreatorUserID: uuid.New(),
		Reason:        teamprovision.ReasonDefaultSignupTeam,
	}
}

// provisioning runs in the background: the call returns before the (blocked)
// sink completes, and the creator context is resolved off-path into the request.
func TestProvisionTeamInBackgroundIsAsync(t *testing.T) {
	t.Parallel()

	sink := &channelProvisionSink{
		called: make(chan teamprovision.TeamBillingProvisionRequestedV1, 1),
		block:  make(chan struct{}),
	}
	store := &APIStore{teamProvisionSink: sink}

	profile := newTestProfile()
	req := newTestProvisionRequest()

	store.provisionTeamInBackground(context.Background(), profile, req)

	// The call returned while the sink is still blocked, so nothing provisioned yet.
	select {
	case <-sink.called:
		t.Fatal("provisioning ran synchronously; expected it to wait on the background sink")
	default:
	}

	close(sink.block)

	select {
	case got := <-sink.called:
		require.Equal(t, req.TeamID, got.TeamID)
		require.Equal(t, req.CreatorUserID, got.CreatorUserID)
		require.Equal(t, req.Reason, got.Reason)
		require.NotNil(t, got.CreatorContext, "creator context should be resolved in the background")
		require.Equal(t, "1.2.3.4", got.CreatorContext.IPAddress)
	case <-time.After(2 * time.Second):
		t.Fatal("background provisioning never ran")
	}
}

// The background work must survive cancellation of the originating request.
func TestProvisionTeamInBackgroundDetachesCancellation(t *testing.T) {
	t.Parallel()

	sink := &channelProvisionSink{called: make(chan teamprovision.TeamBillingProvisionRequestedV1, 1)}
	store := &APIStore{teamProvisionSink: sink}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // request context is already done before the goroutine runs

	store.provisionTeamInBackground(ctx, newTestProfile(), newTestProvisionRequest())

	select {
	case <-sink.called:
	case <-time.After(2 * time.Second):
		t.Fatal("background provisioning did not run after the request context was cancelled")
	}
}

// A failing sink is logged, not propagated, and must not crash the goroutine.
func TestProvisionTeamInBackgroundSwallowsFailure(t *testing.T) {
	t.Parallel()

	sink := &channelProvisionSink{
		called: make(chan teamprovision.TeamBillingProvisionRequestedV1, 1),
		err:    errors.New("billing unavailable"),
	}
	store := &APIStore{teamProvisionSink: sink}

	store.provisionTeamInBackground(context.Background(), newTestProfile(), newTestProvisionRequest())

	select {
	case <-sink.called:
	case <-time.After(2 * time.Second):
		t.Fatal("background provisioning never ran")
	}
}
