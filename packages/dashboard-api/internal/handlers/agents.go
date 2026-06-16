package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/auth/pkg/auth"
	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
	dashboardqueries "github.com/e2b-dev/infra/packages/db/pkg/dashboard/queries"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	"github.com/e2b-dev/infra/packages/shared/pkg/ginutils"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (s *APIStore) GetAgents(c *gin.Context) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "list agents")
	teamID := auth.MustGetTeamID(c)
	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamID.String()))

	rows, err := s.db.Dashboard.ListAgents(ctx, teamID)
	if err != nil {
		logger.L().Error(ctx, "failed to list agents", zap.Error(err))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to list agents")

		return
	}

	agents := make([]api.Agent, 0, len(rows))
	for _, row := range rows {
		agent, err := agentFromFields(agentFields{
			id:          row.ID,
			teamID:      row.TeamID,
			name:        row.Name,
			templateID:  row.TemplateID,
			description: row.Description,
			command:     row.Command,
			author:      row.Author,
			metadata:    row.Metadata,
			public:      row.Public,
			createdAt:   row.CreatedAt,
			updatedAt:   row.UpdatedAt,
			deletedAt:   row.DeletedAt,
		})
		if err != nil {
			logger.L().Error(ctx, "failed to parse agent metadata", zap.Error(err), logger.WithTeamID(teamID.String()))
			s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to list agents")

			return
		}

		agents = append(agents, agent)
	}

	c.JSON(http.StatusOK, api.AgentsResponse{
		Agents: agents,
	})
}

func (s *APIStore) PostAgents(c *gin.Context) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "create agent")
	teamID := auth.MustGetTeamID(c)
	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamID.String()))

	body, err := ginutils.ParseBody[api.CreateAgentRequest](ctx, c)
	if err != nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid request body")

		return
	}

	if strings.TrimSpace(body.Name) == "" || strings.TrimSpace(body.Template) == "" || strings.TrimSpace(body.Description) == "" {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Name, template, and description are required")

		return
	}

	metadata, err := marshalAgentMetadata(body.Metadata)
	if err != nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid metadata")

		return
	}

	public := false
	if body.Public != nil {
		public = *body.Public
	}

	row, err := s.db.Dashboard.CreateAgent(ctx, dashboardqueries.CreateAgentParams{
		TeamID:      teamID,
		Name:        body.Name,
		TemplateID:  body.Template,
		Description: body.Description,
		Command:     body.Command,
		Author:      body.Author,
		Metadata:    metadata,
		Public:      public,
	})
	if err != nil {
		if dberrors.IsUniqueConstraintViolation(err) {
			s.sendAPIStoreError(c, http.StatusConflict, "Agent already exists for this template")

			return
		}

		logger.L().Error(ctx, "failed to create agent", zap.Error(err), logger.WithTeamID(teamID.String()))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to create agent")

		return
	}

	agent, err := agentFromFields(agentFields{
		id:          row.ID,
		teamID:      row.TeamID,
		name:        row.Name,
		templateID:  row.TemplateID,
		description: row.Description,
		command:     row.Command,
		author:      row.Author,
		metadata:    row.Metadata,
		public:      row.Public,
		createdAt:   row.CreatedAt,
		updatedAt:   row.UpdatedAt,
		deletedAt:   row.DeletedAt,
	})
	if err != nil {
		logger.L().Error(ctx, "failed to parse created agent metadata", zap.Error(err), logger.WithTeamID(teamID.String()))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to create agent")

		return
	}

	c.JSON(http.StatusCreated, agent)
}

func (s *APIStore) PatchAgentsAgentID(c *gin.Context, agentID api.AgentID) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "update agent")
	teamID := auth.MustGetTeamID(c)
	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamID.String()))

	body, err := ginutils.ParseBodyWith(ctx, c, parseUpdateAgentBody)
	if err != nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid request body")

		return
	}

	if !body.hasUpdates() {
		s.sendAPIStoreError(c, http.StatusBadRequest, "At least one field must be provided")

		return
	}

	if body.NameSet && strings.TrimSpace(body.Name) == "" {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Name must not be empty")

		return
	}

	if body.TemplateSet && strings.TrimSpace(body.Template) == "" {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Template must not be empty")

		return
	}

	if body.DescriptionSet && strings.TrimSpace(body.Description) == "" {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Description must not be empty")

		return
	}

	metadata, err := marshalAgentMetadata(body.Metadata)
	if err != nil {
		s.sendAPIStoreError(c, http.StatusBadRequest, "Invalid metadata")

		return
	}

	row, err := s.db.Dashboard.UpdateAgent(ctx, dashboardqueries.UpdateAgentParams{
		ID:             uuid.UUID(agentID),
		TeamID:         teamID,
		NameSet:        body.NameSet,
		Name:           body.Name,
		TemplateIDSet:  body.TemplateSet,
		TemplateID:     body.Template,
		DescriptionSet: body.DescriptionSet,
		Description:    body.Description,
		CommandSet:     body.CommandSet,
		Command:        body.Command,
		AuthorSet:      body.AuthorSet,
		Author:         body.Author,
		MetadataSet:    body.MetadataSet,
		Metadata:       metadata,
		PublicSet:      body.PublicSet,
		Public:         body.Public,
	})
	if err != nil {
		if dberrors.IsNotFoundError(err) {
			s.sendAPIStoreError(c, http.StatusNotFound, "Agent not found")

			return
		}

		if dberrors.IsUniqueConstraintViolation(err) {
			s.sendAPIStoreError(c, http.StatusConflict, "Agent already exists for this template")

			return
		}

		logger.L().Error(ctx, "failed to update agent", zap.Error(err), logger.WithTeamID(teamID.String()))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to update agent")

		return
	}

	agent, err := agentFromFields(agentFields{
		id:          row.ID,
		teamID:      row.TeamID,
		name:        row.Name,
		templateID:  row.TemplateID,
		description: row.Description,
		command:     row.Command,
		author:      row.Author,
		metadata:    row.Metadata,
		public:      row.Public,
		createdAt:   row.CreatedAt,
		updatedAt:   row.UpdatedAt,
		deletedAt:   row.DeletedAt,
	})
	if err != nil {
		logger.L().Error(ctx, "failed to parse updated agent metadata", zap.Error(err), logger.WithTeamID(teamID.String()))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to update agent")

		return
	}

	c.JSON(http.StatusOK, agent)
}

func (s *APIStore) DeleteAgentsAgentID(c *gin.Context, agentID api.AgentID) {
	ctx := c.Request.Context()
	telemetry.ReportEvent(ctx, "delete agent")
	teamID := auth.MustGetTeamID(c)
	telemetry.SetAttributes(ctx, telemetry.WithTeamID(teamID.String()))

	rowsAffected, err := s.db.Dashboard.DeleteAgent(ctx, dashboardqueries.DeleteAgentParams{
		ID:     uuid.UUID(agentID),
		TeamID: teamID,
	})
	if err != nil {
		logger.L().Error(ctx, "failed to delete agent", zap.Error(err), logger.WithTeamID(teamID.String()))
		s.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to delete agent")

		return
	}

	if rowsAffected == 0 {
		s.sendAPIStoreError(c, http.StatusNotFound, "Agent not found")

		return
	}

	c.Status(http.StatusNoContent)
	c.Writer.WriteHeaderNow()
}

type agentFields struct {
	id          uuid.UUID
	teamID      *uuid.UUID
	name        string
	templateID  string
	description string
	command     *string
	author      *string
	metadata    []byte
	public      bool
	createdAt   time.Time
	updatedAt   time.Time
	deletedAt   *time.Time
}

func agentFromFields(fields agentFields) (api.Agent, error) {
	metadata, err := unmarshalAgentMetadata(fields.metadata)
	if err != nil {
		return api.Agent{}, err
	}

	return api.Agent{
		Id:          fields.id,
		TeamId:      fields.teamID,
		Name:        fields.name,
		Template:    fields.templateID,
		Description: fields.description,
		Command:     fields.command,
		Author:      fields.author,
		Metadata:    metadata,
		Public:      fields.public,
		CreatedAt:   fields.createdAt,
		UpdatedAt:   fields.updatedAt,
		DeletedAt:   fields.deletedAt,
	}, nil
}

func marshalAgentMetadata(metadata *api.AgentMetadata) (*string, error) {
	if metadata == nil {
		return nil, nil
	}

	raw, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}

	value := string(raw)
	return &value, nil
}

func unmarshalAgentMetadata(raw []byte) (api.AgentMetadata, error) {
	if len(raw) == 0 {
		return api.AgentMetadata{}, nil
	}

	var metadata api.AgentMetadata
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return nil, err
	}

	if metadata == nil {
		return api.AgentMetadata{}, nil
	}

	return metadata, nil
}

type updateAgentBody struct {
	NameSet        bool
	Name           string
	TemplateSet    bool
	Template       string
	DescriptionSet bool
	Description    string
	CommandSet     bool
	Command        *string
	AuthorSet      bool
	Author         *string
	MetadataSet    bool
	Metadata       *api.AgentMetadata
	PublicSet      bool
	Public         bool
}

func (b updateAgentBody) hasUpdates() bool {
	return b.NameSet ||
		b.TemplateSet ||
		b.DescriptionSet ||
		b.CommandSet ||
		b.AuthorSet ||
		b.MetadataSet ||
		b.PublicSet
}

func parseUpdateAgentBody(bodyReader io.Reader) (updateAgentBody, error) {
	var body updateAgentBody

	var payload map[string]json.RawMessage
	decoder := json.NewDecoder(bodyReader)
	if err := decoder.Decode(&payload); err != nil {
		return body, err
	}

	for field := range payload {
		switch field {
		case "name", "template", "description", "command", "author", "metadata", "public":
		default:
			return body, errors.New("unknown field")
		}
	}

	if raw, ok := payload["name"]; ok {
		body.NameSet = true
		value, err := parseRequiredString(raw)
		if err != nil {
			return body, err
		}
		body.Name = value
	}

	if raw, ok := payload["template"]; ok {
		body.TemplateSet = true
		value, err := parseRequiredString(raw)
		if err != nil {
			return body, err
		}
		body.Template = value
	}

	if raw, ok := payload["description"]; ok {
		body.DescriptionSet = true
		value, err := parseRequiredString(raw)
		if err != nil {
			return body, err
		}
		body.Description = value
	}

	if raw, ok := payload["command"]; ok {
		body.CommandSet = true
		value, err := parseNullableString(raw)
		if err != nil {
			return body, err
		}
		body.Command = value
	}

	if raw, ok := payload["author"]; ok {
		body.AuthorSet = true
		value, err := parseNullableString(raw)
		if err != nil {
			return body, err
		}
		body.Author = value
	}

	if raw, ok := payload["metadata"]; ok {
		body.MetadataSet = true
		if bytes.Equal(raw, []byte("null")) {
			return body, errors.New("metadata cannot be null")
		}

		var metadata api.AgentMetadata
		if err := json.Unmarshal(raw, &metadata); err != nil {
			return body, err
		}
		body.Metadata = &metadata
	}

	if raw, ok := payload["public"]; ok {
		body.PublicSet = true
		if bytes.Equal(raw, []byte("null")) {
			return body, errors.New("public cannot be null")
		}
		if err := json.Unmarshal(raw, &body.Public); err != nil {
			return body, err
		}
	}

	return body, nil
}

func parseRequiredString(raw json.RawMessage) (string, error) {
	if bytes.Equal(raw, []byte("null")) {
		return "", errors.New("field cannot be null")
	}

	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}

	return value, nil
}

func parseNullableString(raw json.RawMessage) (*string, error) {
	if bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}

	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}

	return &value, nil
}
