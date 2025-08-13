package handlers

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/constants"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envalias"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/e2b-dev/infra/packages/shared/pkg/schema"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type BuildTemplateRequest struct {
	ClusterID     *uuid.UUID
	BuilderNodeID string
	TemplateID    api.TemplateID
	IsNew         bool
	UserID        *uuid.UUID
	Team          *queries.Team
	Tier          *queries.Tier
	Dockerfile    string
	Alias         *string
	StartCmd      *string
	ReadyCmd      *string
	CpuCount      *int32
	MemoryMB      *int32
}

type TemplateBuildResponse struct {
	TemplateID         string
	BuildID            string
	Public             bool
	Aliases            *[]string
	KernelVersion      string
	FirecrackerVersion string
	StartCmd           *string
	ReadyCmd           *string
	VCpu               int64
	MemoryMB           int64
	FreeDiskSizeMB     int64
}

func (a *APIStore) PostTemplates(c *gin.Context) {
	ctx := c.Request.Context()
	envID := id.Generate()

	telemetry.ReportEvent(ctx, "started creating new environment")

	template := a.TemplateRequestBuild(c, envID, true)
	if template != nil {
		c.JSON(http.StatusAccepted, &template)
	}
}

func (a *APIStore) PostTemplatesTemplateID(c *gin.Context, templateID api.TemplateID) {
	cleanedTemplateID, err := id.CleanEnvID(templateID)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid template ID: %s", cleanedTemplateID))

		telemetry.ReportCriticalError(c.Request.Context(), "invalid template ID", err)

		return
	}

	template := a.TemplateRequestBuild(c, cleanedTemplateID, false)

	if template != nil {
		c.JSON(http.StatusAccepted, &template)
	}
}

func (a *APIStore) BuildTemplate(ctx context.Context, req BuildTemplateRequest) (*TemplateBuildResponse, *api.APIError) {
	ctx, span := a.Tracer.Start(ctx, "build-template-request")
	defer span.End()

	public := false
	if !req.IsNew {
		// Check if the user has access to the template
		aliasOrTemplateID := req.TemplateID
		if req.Alias != nil {
			aliasOrTemplateID = *req.Alias
		}

		template, err := a.sqlcDB.GetTemplateByID(ctx, req.TemplateID)
		if err != nil {
			telemetry.ReportCriticalError(ctx, "error when getting template", err, telemetry.WithTemplateID(req.TemplateID), telemetry.WithTeamID(req.Team.ID.String()))
			return nil, &api.APIError{
				Err:       err,
				ClientMsg: fmt.Sprintf("Template '%s' not found", aliasOrTemplateID),
				Code:      http.StatusNotFound,
			}
		}

		if template.TeamID != req.Team.ID {
			return nil, &api.APIError{
				Err:       err,
				ClientMsg: fmt.Sprintf("Template '%s' is not accessible for the team '%s'", aliasOrTemplateID, req.Team.ID.String()),
				Code:      http.StatusForbidden,
			}
		}

		public = template.Public
		telemetry.ReportEvent(ctx, "checked user access to template")
	}

	// Generate a build id for the new build
	buildID, err := uuid.NewRandom()
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when generating build id", err)
		return nil, &api.APIError{
			Err:       err,
			ClientMsg: "Failed to generate build id",
			Code:      http.StatusInternalServerError,
		}
	}

	telemetry.SetAttributes(ctx,
		attribute.String("env.team.id", req.Team.ID.String()),
		attribute.String("env.team.name", req.Team.Name),
		telemetry.WithTemplateID(req.TemplateID),
		attribute.String("env.team.tier", req.Team.Tier),
		telemetry.WithBuildID(buildID.String()),
		attribute.String("env.dockerfile", req.Dockerfile),
	)

	if req.Alias != nil {
		telemetry.SetAttributes(ctx, attribute.String("env.alias", *req.Alias))
	}
	if req.StartCmd != nil {
		telemetry.SetAttributes(ctx, attribute.String("env.start_cmd", *req.StartCmd))
	}

	if req.ReadyCmd != nil {
		telemetry.SetAttributes(ctx, attribute.String("env.ready_cmd", *req.ReadyCmd))
	}

	if req.CpuCount != nil {
		telemetry.SetAttributes(ctx, attribute.Int("env.cpu", int(*req.CpuCount)))
	}

	if req.MemoryMB != nil {
		telemetry.SetAttributes(ctx, attribute.Int("env.memory_mb", int(*req.MemoryMB)))
	}

	cpuCount, ramMB, apiError := getCPUAndRAM(req.Tier, req.CpuCount, req.MemoryMB)
	if apiError != nil {
		telemetry.ReportCriticalError(ctx, "error when getting CPU and RAM", apiError.Err)
		return nil, apiError
	}

	var alias string
	if req.Alias != nil {
		alias, err = id.CleanEnvID(*req.Alias)
		if err != nil {
			telemetry.ReportCriticalError(ctx, "invalid alias", err)
			return nil, &api.APIError{
				Err:       err,
				ClientMsg: fmt.Sprintf("Invalid alias: %s", alias),
				Code:      http.StatusBadRequest,
			}
		}
	}

	// Start a transaction to prevent partial updates
	tx, err := a.db.Client.Tx(ctx)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when starting transaction", err)
		return nil, &api.APIError{
			Err:       err,
			ClientMsg: fmt.Sprintf("Error when starting transaction: %s", err),
			Code:      http.StatusInternalServerError,
		}
	}
	defer tx.Rollback()

	// Create the template / or update the build count
	err = tx.
		Env.
		Create().
		SetID(req.TemplateID).
		SetTeamID(req.Team.ID).
		SetNillableCreatedBy(req.UserID).
		SetPublic(false).
		SetNillableClusterID(req.ClusterID).
		OnConflictColumns(env.FieldID).
		UpdateUpdatedAt().
		Update(func(e *models.EnvUpsert) {
			e.AddBuildCount(1)
		}).
		Exec(ctx)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when updating env", err)
		return nil, &api.APIError{
			Err:       err,
			ClientMsg: fmt.Sprintf("Error when updating template: %s", err),
			Code:      http.StatusInternalServerError,
		}
	}
	telemetry.ReportEvent(ctx, "created or update template")

	// Mark the previous not started builds as failed
	err = tx.EnvBuild.Update().Where(
		envbuild.EnvID(req.TemplateID),
		envbuild.StatusEQ(envbuild.StatusWaiting),
	).SetStatus(envbuild.StatusFailed).SetFinishedAt(time.Now()).Exec(ctx)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when updating env", err)
		return nil, &api.APIError{
			Err:       err,
			ClientMsg: fmt.Sprintf("Error when updating template: %s", err),
			Code:      http.StatusInternalServerError,
		}
	}
	telemetry.ReportEvent(ctx, "marked previous builds as failed")

	// Insert the new build
	build, err := tx.EnvBuild.Create().
		SetID(buildID).
		SetEnvID(req.TemplateID).
		SetStatus(envbuild.StatusWaiting).
		SetRAMMB(ramMB).
		SetVcpu(cpuCount).
		SetKernelVersion(schema.DefaultKernelVersion).
		SetFirecrackerVersion(schema.DefaultFirecrackerVersion).
		SetFreeDiskSizeMB(req.Tier.DiskMb).
		SetNillableStartCmd(req.StartCmd).
		SetNillableReadyCmd(req.ReadyCmd).
		SetClusterNodeID(req.BuilderNodeID).
		SetDockerfile(req.Dockerfile).
		Save(ctx)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when inserting build", err)
		return nil, &api.APIError{
			Err:       err,
			ClientMsg: fmt.Sprintf("Error when inserting build: %s", err),
			Code:      http.StatusInternalServerError,
		}
	}
	telemetry.ReportEvent(ctx, "inserted new build")

	// Check if the alias is available and claim it
	if alias != "" {
		envs, err := tx.
			Env.
			Query().
			Where(env.ID(alias)).
			All(ctx)
		if err != nil {
			telemetry.ReportCriticalError(ctx, "error when checking alias", err, attribute.String("alias", alias))
			return nil, &api.APIError{
				Err:       err,
				ClientMsg: fmt.Sprintf("Error when querying alias '%s': %s", alias, err),
				Code:      http.StatusInternalServerError,
			}
		}
		telemetry.ReportEvent(ctx, "checked alias availability")

		if len(envs) > 0 {
			err := fmt.Errorf("alias '%s' is already used", alias)
			telemetry.ReportCriticalError(ctx, "conflict of alias", err, attribute.String("alias", alias))
			return nil, &api.APIError{
				Err:       err,
				ClientMsg: fmt.Sprintf("Alias '%s' is already used", alias),
				Code:      http.StatusConflict,
			}
		}

		aliasDB, err := tx.EnvAlias.Query().Where(envalias.ID(alias)).Only(ctx)
		if err != nil {
			if !models.IsNotFound(err) {
				telemetry.ReportCriticalError(ctx, "error when checking alias", err, attribute.String("alias", alias))
				return nil, &api.APIError{
					Err:       err,
					ClientMsg: fmt.Sprintf("Error when querying for alias: %s", err),
					Code:      http.StatusInternalServerError,
				}
			}

			count, err := tx.EnvAlias.Delete().Where(envalias.EnvID(req.TemplateID), envalias.IsRenamable(true)).Exec(ctx)
			if err != nil {
				telemetry.ReportCriticalError(ctx, "error when deleting template alias", err, attribute.String("alias", alias))
				return nil, &api.APIError{
					Err:       err,
					ClientMsg: fmt.Sprintf("Error when deleting template alias: %s", err),
					Code:      http.StatusInternalServerError,
				}
			}

			if count > 0 {
				telemetry.ReportEvent(ctx, "deleted old aliases", attribute.Int("env.alias.count", count))
			}

			err = tx.
				EnvAlias.
				Create().
				SetEnvID(req.TemplateID).SetIsRenamable(true).SetID(alias).
				Exec(ctx)
			if err != nil {
				telemetry.ReportCriticalError(ctx, "error when inserting alias", err, attribute.String("alias", alias))
				return nil, &api.APIError{
					Err:       err,
					ClientMsg: fmt.Sprintf("Error when inserting alias '%s': %s", alias, err),
					Code:      http.StatusInternalServerError,
				}
			}
			telemetry.ReportEvent(ctx, "created new alias", attribute.String("env.alias", alias))
		} else if aliasDB.EnvID != req.TemplateID {
			err := fmt.Errorf("alias '%s' already used", alias)
			telemetry.ReportCriticalError(ctx, "alias already used", err, attribute.String("alias", alias))
			return nil, &api.APIError{
				Err:       err,
				ClientMsg: fmt.Sprintf("Alias '%s' already used", alias),
				Code:      http.StatusForbidden,
			}
		}

		telemetry.ReportEvent(ctx, "inserted alias", attribute.String("env.alias", alias))
	}

	// Commit the transaction
	err = tx.Commit()
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when committing transaction", err)
		return nil, &api.APIError{
			Err:       err,
			ClientMsg: fmt.Sprintf("Error when committing transaction: %s", err),
			Code:      http.StatusInternalServerError,
		}
	}
	telemetry.ReportEvent(ctx, "committed transaction")

	telemetry.SetAttributes(ctx,
		attribute.String("env.alias", alias),
		attribute.Int64("build.cpu_count", cpuCount),
		attribute.Int64("build.ram_mb", ramMB),
	)
	telemetry.ReportEvent(ctx, "started updating environment")

	var aliases []string
	if alias != "" {
		aliases = append(aliases, alias)
	}

	zap.L().Info("template build requested", logger.WithTemplateID(req.TemplateID), logger.WithBuildID(buildID.String()))

	return &TemplateBuildResponse{
		TemplateID:         *build.EnvID,
		BuildID:            build.ID.String(),
		Public:             public,
		Aliases:            &aliases,
		KernelVersion:      build.KernelVersion,
		FirecrackerVersion: build.FirecrackerVersion,
		StartCmd:           build.StartCmd,
		ReadyCmd:           build.ReadyCmd,
		VCpu:               build.Vcpu,
		MemoryMB:           build.RAMMB,
		FreeDiskSizeMB:     build.FreeDiskSizeMB,
	}, nil
}

// findTeamAndTier finds the appropriate team and tier based on the provided teamID or returns the default team
func findTeamAndTier(teams []queries.GetTeamsWithUsersTeamsWithTierRow, teamID *string) (*queries.Team, *queries.Tier, error) {
	if teamID != nil {
		teamUUID, err := uuid.Parse(*teamID)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid team ID: %s", *teamID)
		}

		for _, t := range teams {
			if t.Team.ID == teamUUID {
				return &t.Team, &t.Tier, nil
			}
		}

		return nil, nil, fmt.Errorf("team '%s' not found", *teamID)
	}

	// Find default team
	for _, t := range teams {
		if t.UsersTeam.IsDefault {
			return &t.Team, &t.Tier, nil
		}
	}

	return nil, nil, fmt.Errorf("default team not found")
}

func (a *APIStore) TemplateRequestBuild(c *gin.Context, templateID api.TemplateID, new bool) *api.Template {
	ctx := c.Request.Context()

	body, err := utils.ParseBody[api.TemplateBuildRequest](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %s", err))
		telemetry.ReportCriticalError(ctx, "invalid request body", err)

		return nil
	}

	// Prepare info for rebuilding env
	userID, teams, err := a.GetUserAndTeams(c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting user: %s", err))

		telemetry.ReportCriticalError(ctx, "error when getting user", err)

		return nil
	}

	// Find the team and tier
	team, tier, err := findTeamAndTier(teams, body.TeamID)
	if err != nil {
		var statusCode int
		if body.TeamID != nil {
			statusCode = http.StatusNotFound
		} else {
			statusCode = http.StatusInternalServerError
		}

		a.sendAPIStoreError(c, statusCode, err.Error())
		telemetry.ReportCriticalError(ctx, "error finding team and tier", err)
		return nil
	}

	builderNodeID, err := a.templateManager.GetAvailableBuildClient(ctx, team.ClusterID)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when getting available build client", err, telemetry.WithTemplateID(templateID))
		a.sendAPIStoreError(c, http.StatusBadRequest, "Error when getting available build client")
		return nil
	}

	// Create the build
	buildReq := BuildTemplateRequest{
		ClusterID:     team.ClusterID,
		BuilderNodeID: builderNodeID,
		TemplateID:    templateID,
		IsNew:         new,
		UserID:        userID,
		Team:          team,
		Tier:          tier,
		Dockerfile:    body.Dockerfile,
		Alias:         body.Alias,
		StartCmd:      body.StartCmd,
		ReadyCmd:      body.ReadyCmd,
		CpuCount:      body.CpuCount,
		MemoryMB:      body.MemoryMB,
	}

	template, apiError := a.BuildTemplate(ctx, buildReq)
	if apiError != nil {
		a.sendAPIStoreError(c, apiError.Code, apiError.ClientMsg)
		telemetry.ReportCriticalError(ctx, "build template request failed", err)
		return nil
	}

	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsUserEvent(userID.String(), team.ID.String(), "submitted environment build request", properties.
		Set("environment", template.TemplateID).
		Set("build_id", template.BuildID).
		Set("alias", body.Alias),
	)

	return &api.Template{
		TemplateID: template.TemplateID,
		BuildID:    template.BuildID,
		Public:     template.Public,
		Aliases:    template.Aliases,
	}
}

func getCPUAndRAM(tier *queries.Tier, cpuCount, memoryMB *int32) (int64, int64, *api.APIError) {
	cpu := constants.DefaultTemplateCPU
	ramMB := constants.DefaultTemplateMemory

	if cpuCount != nil {
		cpu = int64(*cpuCount)
		if cpu < constants.MinTemplateCPU {
			return 0, 0, &api.APIError{
				Err:       fmt.Errorf("CPU count must be at least %d", constants.MinTemplateCPU),
				ClientMsg: fmt.Sprintf("CPU count must be at least %d", constants.MinTemplateCPU),
				Code:      http.StatusBadRequest,
			}
		}

		if cpu > tier.MaxVcpu {
			return 0, 0, &api.APIError{
				Err:       fmt.Errorf("CPU count exceeds team limits (%d)", tier.MaxVcpu),
				ClientMsg: fmt.Sprintf("CPU count can't be higher than %d (if you need to increase this limit, please contact support)", tier.MaxVcpu),
				Code:      http.StatusBadRequest,
			}
		}

	}

	if memoryMB != nil {
		ramMB = int64(*memoryMB)

		if ramMB < constants.MinTemplateMemory {
			return 0, 0, &api.APIError{
				Err:       fmt.Errorf("memory must be at least %d MiB", constants.MinTemplateMemory),
				ClientMsg: fmt.Sprintf("Memory must be at least %d MiB", constants.MinTemplateMemory),
				Code:      http.StatusBadRequest,
			}
		}

		if ramMB%2 != 0 {
			return 0, 0, &api.APIError{
				Err:       fmt.Errorf("user provided memory size isn't divisible by 2"),
				ClientMsg: "Memory must be divisible by 2",
				Code:      http.StatusBadRequest,
			}
		}

		if ramMB > tier.MaxRamMb {
			return 0, 0, &api.APIError{
				Err:       fmt.Errorf("memory exceeds team limits (%d MiB)", tier.MaxRamMb),
				ClientMsg: fmt.Sprintf("Memory can't be higher than %d MiB (if you need to increase this limit, please contact support)", tier.MaxRamMb),
				Code:      http.StatusBadRequest,
			}
		}
	}

	return cpu, ramMB, nil
}
