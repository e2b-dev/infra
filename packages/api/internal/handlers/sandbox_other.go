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
	_ context.Context,
	_ string,
	_ time.Duration,
	_,
	_ map[string]string,
	_ string,
	_ authcache.AuthTeamInfo,
	_ *models.EnvBuild,
	_ *http.Header,
	_ bool,
	_ *string,
	_ string,
	_ bool,
) (*api.Sandbox, *api.APIError) {
	return nil, &api.APIError{
		Err: errors.New("sandbox creation not supported on this platform"),
	}
}
