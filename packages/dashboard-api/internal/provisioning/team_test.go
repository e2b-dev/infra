package provisioning

import (
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	internalteamprovision "github.com/e2b-dev/infra/packages/dashboard-api/internal/teamprovision"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
)

func TestCreateTeam_RecentUserCreatesUnblockedTeam(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	userID := createTestUserAt(t, testDB, time.Now().Add(-time.Hour))

	svc := New(testDB.AuthDB, testIdentityProvider{}, &fakeTeamProvisionSink{})

	team, err := svc.CreateTeam(ctx, userID, "Acme")
	if err != nil {
		t.Fatalf("expected team creation to succeed for recent user, got %v", err)
	}
	if team.IsBlocked {
		t.Fatal("expected recent user team to remain unblocked")
	}
	if team.BlockedReason != nil {
		t.Fatalf("expected nil blocked reason, got %v", team.BlockedReason)
	}
}

func TestCreateTeam_ConcurrentRequestsRespectLocalPolicyWithZeroMemberships(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	userID := createTestUser(t, testDB)

	existingTeam, err := testDB.AuthDB.Write.GetDefaultTeamByUserID(ctx, userID)
	if err != nil {
		t.Fatalf("expected default team: %v", err)
	}
	if err := testDB.AuthDB.Write.DeleteTeamByID(ctx, existingTeam.ID); err != nil {
		t.Fatalf("failed to remove default team: %v", err)
	}

	svc := New(testDB.AuthDB, testIdentityProvider{}, &fakeTeamProvisionSink{})

	var wg sync.WaitGroup
	results := make(chan error, 4)

	for _, name := range []string{"Acme-1", "Acme-2", "Acme-3", "Acme-4"} {
		wg.Add(1)
		go func(teamName string) {
			defer wg.Done()
			_, err := svc.CreateTeam(ctx, userID, teamName)
			results <- err
		}(name)
	}

	wg.Wait()
	close(results)

	var successCount int
	var badRequestCount int
	for err := range results {
		if err == nil {
			successCount++

			continue
		}

		var provisionErr *internalteamprovision.ProvisionError
		if !errors.As(err, &provisionErr) {
			t.Fatalf("expected provisioning error, got %T: %v", err, err)
		}
		if provisionErr.StatusCode == http.StatusBadRequest {
			badRequestCount++

			continue
		}

		t.Fatalf("expected bad request or success, got %d", provisionErr.StatusCode)
	}

	if successCount != maxTeamsPerUser {
		t.Fatalf("expected %d successes, got %d", maxTeamsPerUser, successCount)
	}
	if badRequestCount != 1 {
		t.Fatalf("expected one bad request, got %d", badRequestCount)
	}
}
