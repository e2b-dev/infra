package provisioning

import (
	"errors"
	"fmt"
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

func provisionErrorMessage(t *testing.T, err error) string {
	t.Helper()

	if err == nil {
		t.Fatal("expected team creation to fail")
	}

	var provisionErr *internalteamprovision.ProvisionError
	if !errors.As(err, &provisionErr) {
		t.Fatalf("expected provisioning error, got %T: %v", err, err)
	}
	if provisionErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, provisionErr.StatusCode)
	}

	return provisionErr.Message
}

func TestCreateTeam_BaseTierLimitReturnsUpgradeMessage(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	userID := createTestUser(t, testDB)

	svc := New(testDB.AuthDB, testIdentityProvider{}, &fakeTeamProvisionSink{})

	for i := 1; i < maxTeamsPerUser; i++ {
		if _, err := svc.CreateTeam(ctx, userID, fmt.Sprintf("Acme-%d", i)); err != nil {
			t.Fatalf("expected team creation %d to succeed, got %v", i, err)
		}
	}

	_, err := svc.CreateTeam(ctx, userID, "Acme-over-limit")
	expected := fmt.Sprintf(
		"You can't create more than %d projects, you can upgrade to Pro tier to create up to %d projects",
		maxTeamsPerUser,
		maxTeamsPerUserWithProTier,
	)
	if message := provisionErrorMessage(t, err); message != expected {
		t.Fatalf("expected limit message %q, got %q", expected, message)
	}
}

func TestCreateTeam_ProTierLimitReturnsLimitMessage(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	userID := createTestUser(t, testDB)

	if err := testDB.AuthDB.TestsRawSQL(ctx, `
		INSERT INTO public.tiers (
			id, name, disk_mb, concurrent_instances, max_length_hours,
			default_free_disk_size_mb, max_disk_size_mb
		)
		VALUES ('pro_test_v1', 'Pro test tier', 10240, 20, 24, 8000, 30000)
	`); err != nil {
		t.Fatalf("failed to create pro tier: %v", err)
	}

	defaultTeam, err := testDB.AuthDB.Read.GetDefaultTeamByUserID(ctx, userID)
	if err != nil {
		t.Fatalf("expected default team: %v", err)
	}
	if err := testDB.AuthDB.TestsRawSQL(ctx,
		`UPDATE public.teams SET tier = 'pro_test_v1' WHERE id = $1`,
		defaultTeam.ID,
	); err != nil {
		t.Fatalf("failed to upgrade team tier: %v", err)
	}

	svc := New(testDB.AuthDB, testIdentityProvider{}, &fakeTeamProvisionSink{})

	for i := 1; i < maxTeamsPerUserWithProTier; i++ {
		if _, err := svc.CreateTeam(ctx, userID, fmt.Sprintf("Acme-%d", i)); err != nil {
			t.Fatalf("expected team creation %d to succeed, got %v", i, err)
		}
	}

	_, err = svc.CreateTeam(ctx, userID, "Acme-over-limit")
	expected := fmt.Sprintf("You can't create more than %d projects", maxTeamsPerUserWithProTier)
	if message := provisionErrorMessage(t, err); message != expected {
		t.Fatalf("expected limit message %q, got %q", expected, message)
	}
}

func TestCreateTeam_BannedTeamReturnsSupportMessage(t *testing.T) {
	t.Parallel()

	testDB := testutils.SetupDatabase(t)
	ctx := t.Context()
	userID := createTestUser(t, testDB)

	defaultTeam, err := testDB.AuthDB.Read.GetDefaultTeamByUserID(ctx, userID)
	if err != nil {
		t.Fatalf("expected default team: %v", err)
	}
	if err := testDB.AuthDB.TestsRawSQL(ctx,
		`UPDATE public.teams SET is_banned = true WHERE id = $1`,
		defaultTeam.ID,
	); err != nil {
		t.Fatalf("failed to ban team: %v", err)
	}

	svc := New(testDB.AuthDB, testIdentityProvider{}, &fakeTeamProvisionSink{})

	_, err = svc.CreateTeam(ctx, userID, "Acme")
	expected := "You're unable to create a project right now. Please contact support if this persists."
	if message := provisionErrorMessage(t, err); message != expected {
		t.Fatalf("expected banned message %q, got %q", expected, message)
	}
}
