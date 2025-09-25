package template

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	"github.com/e2b-dev/infra/packages/api/internal/team"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envalias"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/e2b-dev/infra/packages/shared/pkg/schema"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	gutils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/api/internal/template")

type RegisterBuildData struct {
	ClusterID     uuid.UUID
	BuilderNodeID string
	TemplateID    api.TemplateID
	UserID        *uuid.UUID
	Team          *queries.Team
	Tier          *queries.Tier
	Dockerfile    string
	Alias         *string
	IsNew         bool
	StartCmd      *string
	ReadyCmd      *string
	CpuCount      *int32
	MemoryMB      *int32
}

type RegisterBuildResponse struct {
	TemplateID string
	BuildID    string
	Aliases    []string
	Public     bool
}

func RegisterBuild(
	ctx context.Context,
	templateBuildsCache *templatecache.TemplatesBuildCache,
	db *db.DB,
	sqlcDB *sqlcdb.Client,
	data RegisterBuildData,
) (*RegisterBuildResponse, *api.APIError) {
	ctx, span := tracer.Start(ctx, "build-template-request")
	defer span.End()

	// Limit concurrent template builds
	teamBuilds := templateBuildsCache.GetRunningBuildsForTeam(data.Team.ID)

	// Exclude the current build if it's a rebuild (it will be cancelled)
	teamBuildsExcludingCurrent := gutils.Filter(teamBuilds, func(item templatecache.TemplateBuildInfo) bool {
		return item.TemplateID != data.TemplateID
	})
	if len(teamBuildsExcludingCurrent) >= int(data.Tier.ConcurrentTemplateBuilds) {
		telemetry.ReportError(ctx, "team has reached max concurrent template builds", nil, telemetry.WithTeamID(data.Team.ID.String()), attribute.Int64("tier.concurrent_template_builds", data.Tier.ConcurrentTemplateBuilds))
		return nil, &api.APIError{
			Code: http.StatusTooManyRequests,
			ClientMsg: fmt.Sprintf(
				"you have reached the maximum number of concurrent template builds (%d). Please wait for existing builds to complete or contact support if you need more concurrent builds.",
				data.Tier.ConcurrentTemplateBuilds),
			Err: fmt.Errorf("team '%s' has reached the maximum number of concurrent template builds (%d)", data.Team.ID, data.Tier.ConcurrentTemplateBuilds),
		}
	}

	public := false
	if !data.IsNew {
		// Check if the user has access to the template
		aliasOrTemplateID := data.TemplateID
		if data.Alias != nil {
			aliasOrTemplateID = *data.Alias
		}

		template, err := sqlcDB.GetTemplateByID(ctx, data.TemplateID)
		if err != nil {
			telemetry.ReportCriticalError(ctx, "error when getting template", err, telemetry.WithTemplateID(data.TemplateID), telemetry.WithTeamID(data.Team.ID.String()))
			return nil, &api.APIError{
				Err:       err,
				ClientMsg: fmt.Sprintf("Template '%s' not found", aliasOrTemplateID),
				Code:      http.StatusNotFound,
			}
		}

		if template.TeamID != data.Team.ID {
			return nil, &api.APIError{
				Err:       fmt.Errorf("template '%s' is not accessible for the team '%s'", aliasOrTemplateID, data.Team.ID.String()),
				ClientMsg: fmt.Sprintf("Template '%s' is not accessible for the team '%s'", aliasOrTemplateID, data.Team.ID.String()),
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
		attribute.String("env.team.id", data.Team.ID.String()),
		attribute.String("env.team.name", data.Team.Name),
		telemetry.WithTemplateID(data.TemplateID),
		attribute.String("env.team.tier", data.Team.Tier),
		telemetry.WithBuildID(buildID.String()),
		attribute.String("env.dockerfile", data.Dockerfile),
	)

	if data.Alias != nil {
		telemetry.SetAttributes(ctx, attribute.String("env.alias", *data.Alias))
	}
	if data.StartCmd != nil {
		telemetry.SetAttributes(ctx, attribute.String("env.start_cmd", *data.StartCmd))
	}

	if data.ReadyCmd != nil {
		telemetry.SetAttributes(ctx, attribute.String("env.ready_cmd", *data.ReadyCmd))
	}

	if data.CpuCount != nil {
		telemetry.SetAttributes(ctx, attribute.Int("env.cpu", int(*data.CpuCount)))
	}

	if data.MemoryMB != nil {
		telemetry.SetAttributes(ctx, attribute.Int("env.memory_mb", int(*data.MemoryMB)))
	}

	cpuCount, ramMB, apiError := team.LimitResources(data.Tier, data.CpuCount, data.MemoryMB)
	if apiError != nil {
		telemetry.ReportCriticalError(ctx, "error when getting CPU and RAM", apiError.Err)
		return nil, apiError
	}

	var alias string
	if data.Alias != nil {
		alias, err = id.CleanEnvID(*data.Alias)
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
	tx, err := db.Client.Tx(ctx)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when starting transaction", err)
		return nil, &api.APIError{
			Err:       err,
			ClientMsg: fmt.Sprintf("Error when starting transaction: %s", err),
			Code:      http.StatusInternalServerError,
		}
	}
	defer tx.Rollback()

	var clusterID *uuid.UUID
	if data.ClusterID != consts.LocalClusterID {
		clusterID = &data.ClusterID
	}

	// Create the template / or update the build count
	err = tx.
		Env.
		Create().
		SetID(data.TemplateID).
		SetTeamID(data.Team.ID).
		SetNillableCreatedBy(data.UserID).
		SetPublic(false).
		SetNillableClusterID(clusterID).
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
		envbuild.EnvID(data.TemplateID),
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
		SetEnvID(data.TemplateID).
		SetStatus(envbuild.StatusWaiting).
		SetRAMMB(ramMB).
		SetVcpu(cpuCount).
		SetKernelVersion(schema.DefaultKernelVersion).
		SetFirecrackerVersion(schema.DefaultFirecrackerVersion).
		SetFreeDiskSizeMB(data.Tier.DiskMb).
		SetNillableStartCmd(data.StartCmd).
		SetNillableReadyCmd(data.ReadyCmd).
		SetClusterNodeID(data.BuilderNodeID).
		SetDockerfile(data.Dockerfile).
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

			count, err := tx.EnvAlias.Delete().Where(envalias.EnvID(data.TemplateID), envalias.IsRenamable(true)).Exec(ctx)
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
				SetEnvID(data.TemplateID).SetIsRenamable(true).SetID(alias).
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
		} else if aliasDB.EnvID != data.TemplateID {
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

	zap.L().Info("template build requested", logger.WithTemplateID(data.TemplateID), logger.WithBuildID(buildID.String()))

	return &RegisterBuildResponse{
		TemplateID: build.EnvID,
		BuildID:    build.ID.String(),
		Aliases:    aliases,
		Public:     public,
	}, nil
}
