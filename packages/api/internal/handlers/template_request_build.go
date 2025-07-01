package handlers

import (
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

func (a *APIStore) TemplateRequestBuild(c *gin.Context, templateID api.TemplateID, new bool) *api.Template {
	ctx := c.Request.Context()

	body, err := utils.ParseBody[api.TemplateBuildRequest](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %s", err))
		telemetry.ReportCriticalError(ctx, "invalid request body", err)

		return nil
	}

	telemetry.ReportEvent(ctx, "started request for environment build")

	var team *queries.Team
	var tier *queries.Tier
	// Prepare info for rebuilding env
	userID, teams, err := a.GetUserAndTeams(c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting user: %s", err))

		telemetry.ReportCriticalError(ctx, "error when getting user", err)

		return nil
	}

	if body.TeamID != nil {
		teamUUID, err := uuid.Parse(*body.TeamID)
		if err != nil {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid team ID: %s", *body.TeamID))

			telemetry.ReportCriticalError(ctx, "invalid team ID", err)

			return nil
		}

		for _, t := range teams {
			if t.Team.ID == teamUUID {
				team = &t.Team
				tier = &t.Tier
				break
			}
		}

		if team == nil {
			a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Team '%s' not found", *body.TeamID))

			telemetry.ReportCriticalError(ctx, "team not found", err)

			return nil
		}
	} else {
		for _, t := range teams {
			if t.UsersTeam.IsDefault {
				team = &t.Team
				tier = &t.Tier
				break
			}
		}

		if team == nil {
			a.sendAPIStoreError(c, http.StatusInternalServerError, "Default team not found")

			telemetry.ReportCriticalError(ctx, "default team not found", err)

			return nil
		}
	}

	if !new {
		// Check if the user has access to the template
		_, err = a.db.Client.Env.Query().Where(env.ID(templateID), env.TeamID(team.ID)).Only(ctx)
		if err != nil {
			a.sendAPIStoreError(c, http.StatusNotFound, fmt.Sprintf("Error when getting template '%s' for team '%s'", templateID, team.ID.String()))

			telemetry.ReportCriticalError(ctx, "error when getting template", err, telemetry.WithTemplateID(templateID), telemetry.WithTeamID(team.ID.String()))

			return nil
		}
	}

	// Generate a build id for the new build
	buildID, err := uuid.NewRandom()
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when generating build id", err)

		a.sendAPIStoreError(c, http.StatusInternalServerError, "Failed to generate build id")

		return nil
	}

	telemetry.SetAttributes(ctx,
		attribute.String("user.id", userID.String()),
		attribute.String("env.team.id", team.ID.String()),
		attribute.String("env.team.name", team.Name),
		telemetry.WithTemplateID(templateID),
		attribute.String("env.team.tier", team.Tier),
		telemetry.WithBuildID(buildID.String()),
		attribute.String("env.dockerfile", body.Dockerfile),
	)

	if body.Alias != nil {
		telemetry.SetAttributes(ctx, attribute.String("env.alias", *body.Alias))
	}
	if body.StartCmd != nil {
		telemetry.SetAttributes(ctx, attribute.String("env.start_cmd", *body.StartCmd))
	}

	if body.ReadyCmd != nil {
		telemetry.SetAttributes(ctx, attribute.String("env.ready_cmd", *body.ReadyCmd))
	}

	if body.CpuCount != nil {
		telemetry.SetAttributes(ctx, attribute.Int("env.cpu", int(*body.CpuCount)))
	}

	if body.MemoryMB != nil {
		telemetry.SetAttributes(ctx, attribute.Int("env.memory_mb", int(*body.MemoryMB)))
	}

	cpuCount, ramMB, apiError := getCPUAndRAM(tier, body.CpuCount, body.MemoryMB)
	if apiError != nil {
		telemetry.ReportCriticalError(ctx, "error when getting CPU and RAM", apiError.Err)
		a.sendAPIStoreError(c, apiError.Code, apiError.ClientMsg)

		return nil
	}

	var alias string
	if body.Alias != nil {
		alias, err = id.CleanEnvID(*body.Alias)
		if err != nil {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Invalid alias: %s", alias))

			telemetry.ReportCriticalError(ctx, "invalid alias", err)

			return nil
		}
	}

	// Start a transaction to prevent partial updates
	tx, err := a.db.Client.Tx(ctx)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when starting transaction: %s", err))

		telemetry.ReportCriticalError(ctx, "error when starting transaction", err)

		return nil
	}
	defer tx.Rollback()

	// Create the template / or update the build count
	err = tx.
		Env.
		Create().
		SetID(templateID).
		SetTeamID(team.ID).
		SetCreatedBy(*userID).
		SetPublic(false).
		SetNillableClusterID(team.ClusterID).
		OnConflictColumns(env.FieldID).
		UpdateUpdatedAt().
		Update(func(e *models.EnvUpsert) {
			e.AddBuildCount(1)
		}).
		Exec(ctx)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when updating template: %s", err))

		telemetry.ReportCriticalError(ctx, "error when updating env", err)

		return nil
	}

	// Mark the previous not started builds as failed
	err = tx.EnvBuild.Update().Where(
		envbuild.EnvID(templateID),
		envbuild.StatusEQ(envbuild.StatusWaiting),
	).SetStatus(envbuild.StatusFailed).SetFinishedAt(time.Now()).Exec(ctx)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when updating template: %s", err))

		telemetry.ReportCriticalError(ctx, "error when updating env", err)

		return nil
	}

	var builderNodeID *string
	if team.ClusterID != nil {
		cluster, found := a.clustersPool.GetClusterById(*team.ClusterID)
		if !found {
			a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Cluster with ID '%s' not found", *team.ClusterID))
			telemetry.ReportCriticalError(ctx, "cluster not found", fmt.Errorf("cluster with ID '%s' not found", *team.ClusterID), telemetry.WithTemplateID(templateID))
			return nil
		}

		clusterNode, err := cluster.GetAvailableTemplateBuilder(ctx)
		if err != nil {
			a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when getting available template builder: %s", err))
			telemetry.ReportCriticalError(ctx, "error when getting available template builder", err, telemetry.WithTemplateID(templateID))
			return nil
		}

		builderNodeID = &clusterNode.NodeID
	}

	// Insert the new build
	err = tx.EnvBuild.Create().
		SetID(buildID).
		SetEnvID(templateID).
		SetStatus(envbuild.StatusWaiting).
		SetRAMMB(ramMB).
		SetVcpu(cpuCount).
		SetKernelVersion(schema.DefaultKernelVersion).
		SetFirecrackerVersion(schema.DefaultFirecrackerVersion).
		SetFreeDiskSizeMB(tier.DiskMb).
		SetNillableStartCmd(body.StartCmd).
		SetNillableReadyCmd(body.ReadyCmd).
		SetNillableClusterNodeID(builderNodeID).
		SetDockerfile(body.Dockerfile).
		Exec(ctx)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when inserting build: %s", err))

		telemetry.ReportCriticalError(ctx, "error when inserting build", err)

		return nil
	}

	// Check if the alias is available and claim it
	if alias != "" {
		envs, err := tx.
			Env.
			Query().
			Where(env.ID(alias)).
			All(ctx)
		if err != nil {
			a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when querying alias '%s': %s", alias, err))

			telemetry.ReportCriticalError(ctx, "error when checking alias", err, attribute.String("alias", alias))

			return nil
		}

		if len(envs) > 0 {
			a.sendAPIStoreError(c, http.StatusConflict, fmt.Sprintf("Alias '%s' is already used", alias))

			telemetry.ReportCriticalError(ctx, "conflict of alias", err, attribute.String("alias", alias))

			return nil
		}

		aliasDB, err := tx.EnvAlias.Query().Where(envalias.ID(alias)).Only(ctx)
		if err != nil {
			if !models.IsNotFound(err) {
				a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when querying for alias: %s", err))

				telemetry.ReportCriticalError(ctx, "error when checking alias", err, attribute.String("alias", alias))

				return nil

			}

			count, err := tx.EnvAlias.Delete().Where(envalias.EnvID(templateID), envalias.IsRenamable(true)).Exec(ctx)
			if err != nil {
				a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when deleting template alias: %s", err))

				telemetry.ReportCriticalError(ctx, "error when deleting template alias", err, attribute.String("alias", alias))

				return nil
			}

			if count > 0 {
				telemetry.ReportEvent(ctx, "deleted old aliases", attribute.Int("env.alias.count", count))
			}

			err = tx.
				EnvAlias.
				Create().
				SetEnvID(templateID).SetIsRenamable(true).SetID(alias).
				Exec(ctx)
			if err != nil {
				a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when inserting alias '%s': %s", alias, err))

				telemetry.ReportCriticalError(ctx, "error when inserting alias", err, attribute.String("alias", alias))

				return nil

			}
		} else if aliasDB.EnvID != templateID {
			a.sendAPIStoreError(c, http.StatusForbidden, fmt.Sprintf("Alias '%s' already used", alias))

			telemetry.ReportCriticalError(ctx, "alias already used", err, attribute.String("alias", alias))

			return nil
		}

		telemetry.ReportEvent(ctx, "inserted alias", attribute.String("env.alias", alias))
	}

	// Commit the transaction
	err = tx.Commit()
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when committing transaction: %s", err))

		telemetry.ReportCriticalError(ctx, "error when committing transaction", err)

		return nil
	}

	properties := a.posthog.GetPackageToPosthogProperties(&c.Request.Header)
	a.posthog.IdentifyAnalyticsTeam(team.ID.String(), team.Name)
	a.posthog.CreateAnalyticsUserEvent(userID.String(), team.ID.String(), "submitted environment build request", properties.
		Set("environment", templateID).
		Set("build_id", buildID).
		Set("alias", alias),
	)

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

	zap.L().Info("Built template", logger.WithTemplateID(templateID), logger.WithBuildID(buildID.String()))

	return &api.Template{
		TemplateID: templateID,
		BuildID:    buildID.String(),
		Public:     false,
		Aliases:    &aliases,
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
