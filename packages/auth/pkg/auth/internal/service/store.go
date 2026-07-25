package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.uber.org/zap"

	internalauthteam "github.com/e2b-dev/infra/packages/auth/pkg/auth/internal/team"
	"github.com/e2b-dev/infra/packages/auth/pkg/types"
	authdb "github.com/e2b-dev/infra/packages/db/pkg/auth"
	authqueries "github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/auth/pkg/auth/internal/service")

// last_used is minute-grade observability metadata, but updating it on every
// authenticated request makes team_api_keys one of the highest dead-tuple
// producers in the registry. One write per key per window keeps it fresh
// enough at a fraction of the churn.
const lastUsedWriteWindow = time.Minute

var (
	lastUsedWrites     sync.Map // api key hash -> time.Time of last write
	lastUsedCallsSweep atomic.Int64
)

func shouldWriteLastUsed(hashedKey string, now time.Time) bool {
	// Occasionally drop entries idle for many windows so the map tracks the
	// working set of keys, not every key ever seen by the process.
	if lastUsedCallsSweep.Add(1)%4096 == 0 {
		lastUsedWrites.Range(func(k, v any) bool {
			if now.Sub(v.(time.Time)) > 10*lastUsedWriteWindow {
				lastUsedWrites.Delete(k)
			}

			return true
		})
	}

	prev, loaded := lastUsedWrites.LoadOrStore(hashedKey, now)
	if !loaded {
		return true
	}
	if now.Sub(prev.(time.Time)) < lastUsedWriteWindow {
		return false
	}

	// CAS so exactly one concurrent caller wins the expired window.
	return lastUsedWrites.CompareAndSwap(hashedKey, prev, now)
}

type authStoreImpl struct {
	authDB *authdb.Client
}

var _ authStore = (*authStoreImpl)(nil)

func newAuthStore(authDB *authdb.Client) *authStoreImpl {
	return &authStoreImpl{authDB: authDB}
}

func (s *authStoreImpl) GetTeamByHashedAPIKey(ctx context.Context, hashedKey string) (*types.Team, error) {
	ctx, span := tracer.Start(ctx, "get team auth")
	defer span.End()

	// Deleting an API key invalidates its cache entry; reading through the
	// read replica here races replication lag and could re-cache a
	// just-deleted key for the full cache TTL, so key revocation must be
	// read-after-write safe.
	result, err := s.authDB.GetTeamWithTierByAPIKey(ctx, hashedKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get team from API key: %w", err)
	}

	if err := internalauthteam.CheckTeamBanned(result.Team); err != nil {
		return nil, err
	}

	if shouldWriteLastUsed(hashedKey, time.Now()) {
		go func() {
			// Run the update in a separate context to avoid an extra latency
			ctx := context.WithoutCancel(ctx)
			updateErr := s.authDB.UpdateLastTimeUsed(ctx, hashedKey)
			if updateErr != nil {
				logger.L().Error(ctx, "failed to update last time used", zap.Error(updateErr))
			}
		}()
	}

	team := types.NewTeam(&result.Team, &result.TeamLimit)

	return team, nil
}

func (s *authStoreImpl) GetTeamByID(ctx context.Context, teamID uuid.UUID) (*types.Team, error) {
	ctx, span := tracer.Start(ctx, "get team by id auth")
	defer span.End()

	result, err := s.authDB.GetTeamWithTierByTeamID(ctx, teamID)
	if err != nil {
		return nil, fmt.Errorf("failed to get team from team ID: %w", err)
	}

	if err := internalauthteam.CheckTeamBanned(result.Team); err != nil {
		return nil, err
	}

	team := types.NewTeam(&result.Team, &result.TeamLimit)

	return team, nil
}

func (s *authStoreImpl) GetTeamByIDAndUserID(ctx context.Context, userID uuid.UUID, teamID string) (*types.Team, error) {
	ctx, span := tracer.Start(ctx, "get team by id and user id auth")
	defer span.End()

	teamIDParsed, err := uuid.Parse(teamID)
	if err != nil {
		return nil, fmt.Errorf("failed to parse team ID: %w", err)
	}

	result, err := s.authDB.GetTeamWithTierByTeamAndUser(ctx, authqueries.GetTeamWithTierByTeamAndUserParams{
		ID:     teamIDParsed,
		UserID: userID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get team from teamID and userID key: %w", err)
	}

	if err := internalauthteam.CheckTeamBanned(result.Team); err != nil {
		return nil, err
	}

	team := types.NewTeam(&result.Team, &result.TeamLimit)

	return team, nil
}

func (s *authStoreImpl) GetUserIDByHashedAccessToken(ctx context.Context, hashedToken string) (uuid.UUID, error) {
	return s.authDB.GetUserIDFromAccessToken(ctx, hashedToken)
}

func (s *authStoreImpl) GetTeamAPIKeyHashes(ctx context.Context, teamID uuid.UUID) ([]string, error) {
	return s.authDB.GetTeamAPIKeyHashes(ctx, teamID)
}
