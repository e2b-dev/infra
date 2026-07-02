package provisioning

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/userprofile"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/testutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

const testIssuer = "https://ory.example.test"

func createTestUser(t *testing.T, db *testutils.Database) uuid.UUID {
	t.Helper()

	createdAt := time.Now()

	return createTestUserWithCreatedAt(t, db, &createdAt)
}

func createTestUserAt(t *testing.T, db *testutils.Database, createdAt time.Time) uuid.UUID {
	t.Helper()

	return createTestUserWithCreatedAt(t, db, &createdAt)
}

func createTestUserWithCreatedAt(t *testing.T, db *testutils.Database, createdAt *time.Time) uuid.UUID {
	t.Helper()

	userID := uuid.New()
	email := testUserEmail(userID)
	_ = createdAt

	if err := db.AuthDB.Write.UpsertPublicUser(t.Context(), userID); err != nil {
		t.Fatalf("failed to create public user: %v", err)
	}

	team, err := db.AuthDB.Write.CreateTeam(t.Context(), authqueries.CreateTeamParams{
		Name:  email,
		Tier:  baseTierID,
		Email: email,
	})
	if err != nil {
		t.Fatalf("failed to create default team: %v", err)
	}

	if err := db.AuthDB.Write.CreateTeamMembership(t.Context(), authqueries.CreateTeamMembershipParams{
		UserID:    userID,
		TeamID:    team.ID,
		IsDefault: true,
		AddedBy:   nil,
	}); err != nil {
		t.Fatalf("failed to create default team membership: %v", err)
	}

	return userID
}

func testUserEmail(userID uuid.UUID) string {
	return "user-" + userID.String() + "@example.com"
}

type testUserProfiles struct{}

func newTestUserProfiles() userprofile.Provider {
	return testUserProfiles{}
}

func (testUserProfiles) GetProfilesByUserID(_ context.Context, userIDs []uuid.UUID) (map[uuid.UUID]userprofile.Profile, error) {
	profiles := make(map[uuid.UUID]userprofile.Profile, len(userIDs))
	for _, userID := range userIDs {
		if userID == uuid.Nil {
			continue
		}
		profiles[userID] = userprofile.Profile{
			UserID: userID,
			Email:  testUserEmail(userID),
		}
	}

	return profiles, nil
}

func (testUserProfiles) FindProfilesByEmail(_ context.Context, email string) ([]userprofile.Profile, error) {
	userIDText := strings.TrimSuffix(strings.TrimPrefix(email, "user-"), "@example.com")
	userID, _ := uuid.Parse(userIDText)
	if userID == uuid.Nil {
		return []userprofile.Profile{}, nil
	}

	return []userprofile.Profile{{
		UserID: userID,
		Email:  email,
	}}, nil
}

func (testUserProfiles) GetTeamCreatorContext(context.Context, uuid.UUID) (*teamprovision.CreatorContextV1, error) {
	return nil, nil
}

func (testUserProfiles) GetIdentityOrganizationID(context.Context, string) (uuid.UUID, error) {
	return uuid.Nil, nil
}

func (testUserProfiles) GetUserOrganizationID(context.Context, uuid.UUID) (uuid.UUID, error) {
	return uuid.Nil, nil
}

func (testUserProfiles) SetIdentityExternalID(context.Context, string, uuid.UUID) error {
	return nil
}

func (testUserProfiles) PrepareDeleteUser(context.Context, uuid.UUID) (userprofile.DeleteUserHandle, error) {
	return nil, nil
}

type fakeTeamProvisionSink struct {
	mu       sync.Mutex
	requests []teamprovision.TeamBillingProvisionRequestedV1
	err      error
}

func (s *fakeTeamProvisionSink) ProvisionTeam(_ context.Context, req teamprovision.TeamBillingProvisionRequestedV1) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.requests = append(s.requests, req)

	return s.err
}
