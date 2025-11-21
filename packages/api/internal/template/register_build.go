package template

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/api/internal/team"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/dberrors"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	gutils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/api/internal/template")

type RegisterBuildData struct {
	ClusterID          uuid.UUID
	TemplateID         api.TemplateID
	UserID             *uuid.UUID
	Team               *types.Team
	Dockerfile         string
	Alias              *string
	StartCmd           *string
	ReadyCmd           *string
	CpuCount           *int32
	MemoryMB           *int32
	Version            string
	KernelVersion      string
	FirecrackerVersion string
}

type RegisterBuildResponse struct {
	TemplateID string
	BuildID    string
	Aliases    []string
}

func RegisterBuild(
	ctx context.Context,
	templateBuildsCache *templatecache.TemplatesBuildCache,
	db *sqlcdb.Client,
	data RegisterBuildData,
) (*RegisterBuildResponse, *api.APIError) {
	ctx, span := tracer.Start(ctx, "register build")
	defer span.End()

	// Limit concurrent template builds
	teamBuilds := templateBuildsCache.GetRunningBuildsForTeam(data.Team.ID)

	// Exclude the current build if it's a rebuild (it will be cancelled)
	teamBuildsExcludingCurrent := gutils.Filter(teamBuilds, func(item templatecache.TemplateBuildInfo) bool {
		return item.TemplateID != data.TemplateID
	})

	totalConcurrentTemplateBuilds := data.Team.Limits.BuildConcurrency
	if len(teamBuildsExcludingCurrent) >= int(totalConcurrentTemplateBuilds) {
		telemetry.ReportError(ctx, "team has reached max concurrent template builds", nil, telemetry.WithTeamID(data.Team.ID.String()), attribute.Int64("total.concurrent_template_builds", totalConcurrentTemplateBuilds))

		return nil, &api.APIError{
			Code: http.StatusTooManyRequests,
			ClientMsg: fmt.Sprintf(
				"you have reached the maximum number of concurrent template builds (%d). Please wait for existing builds to complete or contact support if you need more concurrent builds.",
				totalConcurrentTemplateBuilds),
			Err: fmt.Errorf("team '%s' has reached the maximum number of concurrent template builds (%d)", data.Team.ID, totalConcurrentTemplateBuilds),
		}
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
		attribute.String("env.version", data.Version),
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

	cpuCount, ramMB, apiError := team.LimitResources(data.Team.Limits, data.CpuCount, data.MemoryMB)
	if apiError != nil {
		telemetry.ReportCriticalError(ctx, "error when getting CPU and RAM", apiError.Err)

		return nil, apiError
	}

	var alias string
	if data.Alias != nil {
		alias, err = id.CleanTemplateID(*data.Alias)
		if err != nil {
			telemetry.ReportCriticalError(ctx, "invalid alias", err)

			return nil, &api.APIError{
				Err:       err,
				ClientMsg: fmt.Sprintf("Invalid alias: %s", *data.Alias),
				Code:      http.StatusBadRequest,
			}
		}
	}

	// Start a transaction to prevent partial updates
	client, tx, err := db.WithTx(ctx)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when starting transaction", err)

		return nil, &api.APIError{
			Err:       err,
			ClientMsg: fmt.Sprintf("Error when starting transaction: %s", err),
			Code:      http.StatusInternalServerError,
		}
	}
	defer tx.Rollback(ctx)

	var clusterID *uuid.UUID
	if data.ClusterID != consts.LocalClusterID {
		clusterID = &data.ClusterID
	}

	// Create the template / or update the build count
	err = client.CreateOrUpdateTemplate(ctx, queries.CreateOrUpdateTemplateParams{
		TemplateID: data.TemplateID,
		TeamID:     data.Team.ID,
		CreatedBy:  data.UserID,
		ClusterID:  clusterID,
	})
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
	err = client.InvalidateUnfinishedTemplateBuilds(ctx, data.TemplateID)
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
	err = client.CreateTemplateBuild(ctx, queries.CreateTemplateBuildParams{
		BuildID:            buildID,
		TemplateID:         data.TemplateID,
		RamMb:              ramMB,
		Vcpu:               cpuCount,
		KernelVersion:      data.KernelVersion,
		FirecrackerVersion: data.FirecrackerVersion,
		FreeDiskSizeMb:     data.Team.Limits.DiskMb,
		StartCmd:           data.StartCmd,
		ReadyCmd:           data.ReadyCmd,
		Dockerfile:         gutils.ToPtr(data.Dockerfile),
		Version:            gutils.ToPtr(data.Version),
	})
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
		exists, err := client.CheckAliasConflictsWithTemplate(ctx, alias)
		if err != nil {
			telemetry.ReportCriticalError(ctx, "error when checking alias", err, attribute.String("alias", alias))

			return nil, &api.APIError{
				Err:       err,
				ClientMsg: fmt.Sprintf("Error when querying alias '%s': %s", alias, err),
				Code:      http.StatusInternalServerError,
			}
		}
		telemetry.ReportEvent(ctx, "checked alias availability")

		if exists {
			err := fmt.Errorf("alias '%s' is already used", alias)
			telemetry.ReportCriticalError(ctx, "conflict of alias", err, attribute.String("alias", alias))

			return nil, &api.APIError{
				Err:       err,
				ClientMsg: fmt.Sprintf("Alias '%s' is already used", alias),
				Code:      http.StatusConflict,
			}
		}

		aliasDB, err := client.CheckAliasExists(ctx, alias)
		if err != nil {
			if !dberrors.IsNotFoundError(err) {
				telemetry.ReportCriticalError(ctx, "error when checking alias", err, attribute.String("alias", alias))

				return nil, &api.APIError{
					Err:       err,
					ClientMsg: fmt.Sprintf("Error when querying for alias: %s", err),
					Code:      http.StatusInternalServerError,
				}
			}

			aliases, err := client.DeleteOtherTemplateAliases(ctx, data.TemplateID)
			if err != nil {
				telemetry.ReportCriticalError(ctx, "error when deleting template alias", err, attribute.String("alias", alias))

				return nil, &api.APIError{
					Err:       err,
					ClientMsg: fmt.Sprintf("Error when deleting template alias: %s", err),
					Code:      http.StatusInternalServerError,
				}
			}

			count := len(aliases)
			if count > 0 {
				telemetry.ReportEvent(ctx, "deleted old aliases", attribute.Int("env.alias.count", count))
			}

			err = client.
				CreateTemplateAlias(ctx, queries.CreateTemplateAliasParams{
					Alias:      alias,
					TemplateID: data.TemplateID,
				})
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
	err = tx.Commit(ctx)
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
		TemplateID: data.TemplateID,
		BuildID:    buildID.String(),
		Aliases:    aliases,
	}, nil
}
