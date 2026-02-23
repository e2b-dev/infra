package db

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/auth/pkg/types"
	"github.com/e2b-dev/infra/packages/db/pkg/auth"
	"github.com/e2b-dev/infra/packages/db/pkg/auth/queries"
	sharedauth "github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/api/internal/db")

type TeamForbiddenError = sharedauth.TeamForbiddenError

type TeamBlockedError = sharedauth.TeamBlockedError

func validateTeamUsage(team authqueries.Team) error {
	if team.IsBanned {
		return &TeamForbiddenError{Message: "team is banned"}
	}

	if team.IsBlocked {
		return &TeamBlockedError{Message: "team is blocked"}
	}

	return nil
}
