package auth

import (
	"errors"
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
)

const (
	teamContextKey   string = "team"
	userIDContextKey string = "user_id"
)

var (
	ErrNotFoundInContext = errors.New("not found in context")
	ErrInvalidType       = errors.New("unexpected type")
)

type ginContextValueHelper[T any] struct {
	contextKey string
}

func (g *ginContextValueHelper[T]) set(c *gin.Context, val T) {
	c.Set(g.contextKey, val)
}

func (g *ginContextValueHelper[T]) get(c *gin.Context) (T, error) {
	var t T

	v := c.Value(teamContextKey)
	if v == nil {
		return t, ErrNotFoundInContext
	}

	t, ok := v.(T)
	if !ok {
		return t, fmt.Errorf("%w: wanted %T, got %T",
			ErrInvalidType, t, v)
	}

	return t, nil
}

func (g *ginContextValueHelper[T]) safeGet(c *gin.Context) T {
	v, err := g.get(c)
	if err != nil {
		zap.L().Warn("failed to "+g.contextKey, zap.Error(err))
	}
	return v
}

var (
	teamInfoHelper = ginContextValueHelper[authcache.AuthTeamInfo]{"team"}
	userIDHelper   = ginContextValueHelper[uuid.UUID]{"user_id"}
)

func setTeamInfo(c *gin.Context, teamInfo authcache.AuthTeamInfo) {
	teamInfoHelper.set(c, teamInfo)
}

func GetTeamInfo(c *gin.Context) (authcache.AuthTeamInfo, error) {
	return teamInfoHelper.get(c)
}

func SafeGetTeamInfo(c *gin.Context) authcache.AuthTeamInfo {
	return teamInfoHelper.safeGet(c)
}

func setUserID(c *gin.Context, userID uuid.UUID) {
	userIDHelper.set(c, userID)
}

func GetUserID(c *gin.Context) (uuid.UUID, error) {
	return userIDHelper.get(c)
}

func SafeGetUserID(c *gin.Context) uuid.UUID {
	return userIDHelper.safeGet(c)
}
