//go:build !linux
// +build !linux

package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/e2b-dev/infra/packages/api/internal/api"

	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
)

func (a *APIStore) startSandbox(
	ctx context.Context,
	sandboxID string,
	timeout time.Duration,
	envVars,
	metadata map[string]string,
	alias string,
	team authcache.AuthTeamInfo,
	build *models.EnvBuild,
	requestHeader *http.Header,
	isResume bool,
	clientID *string,
	baseTemplateID string,
	autoPause bool,
) (*api.Sandbox, *api.APIError) {
	return nil, &api.APIError{
		Err: errors.New("sandbox creation not supported on this platform"),
	}
}
