package provisioning

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/teamprovision"
)

func (s *Service) resolveTeamCreatorContext(ctx context.Context, userID uuid.UUID) *teamprovision.CreatorContextV1 {
	if userID == uuid.Nil || s.profiles == nil {
		return nil
	}

	resolveCtx, cancel := context.WithTimeout(ctx, creatorContextResolveTimeout)
	defer cancel()

	creatorContext, err := s.profiles.GetTeamCreatorContext(resolveCtx, userID)
	if err != nil {
		logger.L().Warn(ctx, "failed to resolve creator context for team provisioning",
			zap.String("user_id", userID.String()),
			zap.Error(err),
		)

		return nil
	}

	return normalizeCreatorContext(creatorContext)
}

func (s *Service) teamCreatorContextForProvisioning(ctx context.Context, profile bootstrapUserProfile) *teamprovision.CreatorContextV1 {
	if profile.CreatorContext != nil {
		return normalizeCreatorContext(profile.CreatorContext)
	}

	return s.resolveTeamCreatorContext(ctx, profile.UserID)
}

func creatorContextFromSignupMetadata(signupIP, signupUserAgent, authMethod string) *teamprovision.CreatorContextV1 {
	return normalizeCreatorContext(&teamprovision.CreatorContextV1{
		IPAddress:  signupIP,
		UserAgent:  signupUserAgent,
		AuthMethod: authMethod,
	})
}

func normalizeCreatorContext(creatorContext *teamprovision.CreatorContextV1) *teamprovision.CreatorContextV1 {
	if creatorContext == nil {
		return nil
	}

	ipAddress := strings.TrimSpace(creatorContext.IPAddress)
	if ipAddress == "" {
		return nil
	}

	return &teamprovision.CreatorContextV1{
		IPAddress:  ipAddress,
		UserAgent:  strings.TrimSpace(creatorContext.UserAgent),
		AuthMethod: strings.TrimSpace(creatorContext.AuthMethod),
	}
}
