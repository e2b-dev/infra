package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	dashboardqueries "github.com/e2b-dev/infra/packages/db/pkg/dashboard/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/ginutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (s *APIStore) PatchTeamsTeamID(c *gin.Context, teamID api.TeamID) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "update team")

	teamInfo, ok := s.requireAuthedTeamMatchesPath(c, teamID)
	if !ok {
		return
	}

	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamInfo.Team.ID.String()))

	body, err := ginutils.ParseBodyWith(ctx, c, parseUpdateTeamBody)
	if err != nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid request body")

		return
	}

	if !body.NameSet && !body.ProfilePictureUrlSet {
		s.sendAPIStoreError(c, http.StatusBadRequest, "At least one field must be provided")

		return
	}

	if body.NameSet && strings.TrimSpace(body.Name) == "" {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Name must not be empty")

		return
	}

	row, err := s.db.Dashboard.UpdateTeam(ctx, dashboardqueries.UpdateTeamParams{
		TeamID:               teamInfo.Team.ID,
		Name:                 body.NamePtr(),
		NameSet:              body.NameSet,
		ProfilePictureUrl:    body.ProfilePictureUrl,
		ProfilePictureUrlSet: body.ProfilePictureUrlSet,
	})
	if err != nil {
		logger.L().Error(ctx, "failed to update team", zap.Error(err), logger.WithTeamID(teamInfo.Team.ID.String()))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to update team")

		return
	}

	c.JSON(http.StatusOK, api.UpdateTeamResponse{
		Id:                row.ID,
		Name:              row.Name,
		ProfilePictureUrl: row.ProfilePictureUrl,
	})
}

type updateTeamBody struct {
	NameSet              bool
	Name                 string
	ProfilePictureUrlSet bool
	ProfilePictureUrl    *string
}

func (b updateTeamBody) NamePtr() *string {
	if !b.NameSet {
		return nil
	}

	return &b.Name
}

func parseUpdateTeamBody(bodyReader io.Reader) (updateTeamBody, error) {
	var body updateTeamBody

	var payload map[string]json.RawMessage
	decoder := json.NewDecoder(bodyReader)
	if err := decoder.Decode(&payload); err != nil {
		return body, err
	}

	for field := range payload {
		if field != "name" && field != "profilePictureUrl" {
			return body, errors.New("unknown field")
		}
	}

	nameRaw, hasName := payload["name"]
	if hasName {
		body.NameSet = true
		if bytes.Equal(nameRaw, []byte("null")) {
			return body, errors.New("name cannot be null")
		}

		var name string
		if err := json.Unmarshal(nameRaw, &name); err != nil {
			return body, err
		}

		body.Name = name
	}

	profilePictureURLRaw, hasProfilePictureURL := payload["profilePictureUrl"]
	if hasProfilePictureURL {
		body.ProfilePictureUrlSet = true
		if bytes.Equal(profilePictureURLRaw, []byte("null")) {
			body.ProfilePictureUrl = nil
		} else {
			var profilePictureURL string
			if err := json.Unmarshal(profilePictureURLRaw, &profilePictureURL); err != nil {
				return body, err
			}

			body.ProfilePictureUrl = &profilePictureURL
		}
	}

	return body, nil
}
