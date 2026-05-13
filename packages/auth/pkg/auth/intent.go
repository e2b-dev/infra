package auth

import (
	"errors"
	"fmt"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/auth/pkg/types"
)

// ActionIntent classifies what a request is trying to do to the platform.
// Used by AuthorizeTeam to decide whether a blocked team may proceed.
//
// The taxonomy is deliberately coarse: a route's intent should be obvious
// from its URL. When in doubt, prefer the more restrictive intent.
type ActionIntent string

const (
	// IntentView is a read-only operation that does not consume resources.
	IntentView ActionIntent = "view"
	// IntentCreate provisions a new resource that costs compute or storage.
	IntentCreate ActionIntent = "create"
	// IntentMutate changes an existing resource in a way that may consume
	// additional resources or extend a resource's lifetime.
	IntentMutate ActionIntent = "mutate"
	// IntentDelete removes an existing resource. Always allowed for blocked
	// teams so they can clean up while resolving the block.
	IntentDelete ActionIntent = "delete"
)

const intentContextKey = "team.action_intent"

// SetIntent records the resolved intent for the current request.
// Called by the AuthorizeTeamAccess middleware once it has matched the route.
func SetIntent(c *gin.Context, intent ActionIntent) {
	c.Set(intentContextKey, intent)
}

// GetIntent returns the intent stashed by the AuthorizeTeamAccess middleware,
// if any. Used by handlers that resolve their team late (AccessTokenAuth
// paths) and call AuthorizeTeamCtx after the team is known.
func GetIntent(c *gin.Context) (ActionIntent, bool) {
	return getFromGinContextSafely[ActionIntent](c, intentContextKey)
}

// AuthorizeTeam decides whether team may execute an action with the given
// intent. It is the single policy decision point for blocked-team behavior
// across the API.
//
//   - Banned teams are always denied (defensive — auth should have rejected
//     them earlier).
//   - Non-blocked teams are always allowed.
//   - Blocked teams are denied IntentCreate / IntentMutate (these are the
//     operations that consume new compute or extend resource lifetime) and
//     allowed IntentView / IntentDelete (recovery + cleanup).
//
// Returns *TeamForbiddenError for banned teams and *TeamBlockedError for
// blocked teams. The error message embeds the BlockedReason when available
// so it surfaces in the 403 response.
func AuthorizeTeam(team types.Team, intent ActionIntent) error {
	if team.Team == nil {
		return errors.New("team is nil")
	}

	if team.IsBanned {
		return &TeamForbiddenError{Message: "team is banned"}
	}

	if !team.IsBlocked {
		return nil
	}

	switch intent {
	case IntentView, IntentDelete:
		return nil
	case IntentCreate, IntentMutate:
		return &TeamBlockedError{Message: blockedMessage(team)}
	default:
		// Unknown intent — fail closed.
		return &TeamBlockedError{Message: blockedMessage(team)}
	}
}

// AuthorizeTeamCtx is the context-bound variant of AuthorizeTeam for
// handlers that resolve their team inside the body (typically
// AccessTokenAuth-shared endpoints). It reads the intent stashed by the
// AuthorizeTeamAccess middleware and authorizes the supplied team against it.
//
// If no intent is in the context (e.g. the middleware did not run or the
// route was not registered), the call fails closed by treating the request
// as IntentMutate.
func AuthorizeTeamCtx(c *gin.Context, team *types.Team) error {
	if team == nil {
		return errors.New("team is nil")
	}

	intent, ok := GetIntent(c)
	if !ok {
		intent = IntentMutate
	}

	return AuthorizeTeam(*team, intent)
}

func blockedMessage(team types.Team) string {
	msg := "team is blocked"
	if team.BlockedReason != nil && *team.BlockedReason != "" {
		msg = fmt.Sprintf("%s: %s", msg, *team.BlockedReason)
	}

	return msg
}
