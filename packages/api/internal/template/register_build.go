package template

import (
	"context"
	"fmt"
	"net/http"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	"github.com/e2b-dev/infra/packages/api/internal/db/types"
	"github.com/e2b-dev/infra/packages/api/internal/team"
	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/pkg/dberrors"
	dbtypes "github.com/e2b-dev/infra/packages/db/pkg/types"
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
	Tags               []string
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
	Names      []string
	Tags       []string
}

func RegisterBuild(
	ctx context.Context,
	templateBuildsCache *templatecache.TemplatesBuildCache,
	templateCache *templatecache.TemplateCache,
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

	// Add default tag if no tags are present
	tags := data.Tags
	if len(tags) == 0 {
		tags = []string{id.DefaultTag}
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
	if len(tags) > 0 {
		telemetry.SetAttributes(ctx, attribute.StringSlice("env.tags", tags))
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

	// Mark the previous not started builds as failed for all tags
	err = client.InvalidateUnstartedTemplateBuilds(ctx, queries.InvalidateUnstartedTemplateBuildsParams{
		Reason: dbtypes.BuildReason{
			Message: "The build was canceled because it was superseded by a newer one.",
		},
		TemplateID: data.TemplateID,
		Tags:       tags,
	})
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when invalidating unstarted builds", err, attribute.StringSlice("tags", tags))

		return nil, &api.APIError{
			Err:       err,
			ClientMsg: fmt.Sprintf("Error when updating template: %s", err),
			Code:      http.StatusInternalServerError,
		}
	}
	telemetry.ReportEvent(ctx, "marked previous builds as failed")

	// Insert the new build
	// TODO(ENG-3469): Switch to dbtypes.BuildStatusPending once all consumers are migrated to use Is*() helpers.
	err = client.CreateTemplateBuild(ctx, queries.CreateTemplateBuildParams{
		BuildID:            buildID,
		Status:             string(dbtypes.BuildStatusWaiting),
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
	var aliases, names []string
	if data.Alias != nil {
		// Extract just the alias portion (without namespace) for storage
		// The identifier may be "namespace/alias" or just "alias"
		alias := id.ExtractAlias(*data.Alias)
		aliases = append(aliases, alias)
		names = append(names, id.WithNamespace(data.Team.Slug, alias))

		exists, err := client.CheckAliasConflictsWithTemplateID(ctx, alias)
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

		aliasDB, err := client.CheckAliasExistsInNamespace(ctx, queries.CheckAliasExistsInNamespaceParams{
			Alias:     alias,
			Namespace: &data.Team.Slug,
		})
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
					Namespace:  &data.Team.Slug,
				})
			if err != nil {
				telemetry.ReportCriticalError(ctx, "error when inserting alias", err, attribute.String("alias", alias))

				return nil, &api.APIError{
					Err:       err,
					ClientMsg: fmt.Sprintf("Error when inserting alias '%s': %s", alias, err),
					Code:      http.StatusInternalServerError,
				}
			}

			// Invalidate any cached tombstone for this alias
			templateCache.InvalidateAlias(&data.Team.Slug, alias)

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

	for _, tag := range tags {
		err = client.CreateTemplateBuildAssignment(ctx, queries.CreateTemplateBuildAssignmentParams{
			TemplateID: data.TemplateID,
			BuildID:    buildID,
			Tag:        tag,
		})
		if err != nil {
			telemetry.ReportCriticalError(ctx, "error when adding tag to build", err, attribute.String("tag", tag))

			return nil, &api.APIError{
				Err:       err,
				ClientMsg: fmt.Sprintf("Error when adding tag '%s' to build: %s", tag, err),
				Code:      http.StatusInternalServerError,
			}
		}
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
		attribute.Int64("build.cpu_count", cpuCount),
		attribute.Int64("build.ram_mb", ramMB),
	)
	telemetry.ReportEvent(ctx, "started updating environment")

	logger.L().Info(ctx, "template build requested", logger.WithTemplateID(data.TemplateID), logger.WithBuildID(buildID.String()))

	return &RegisterBuildResponse{
		TemplateID: data.TemplateID,
		BuildID:    buildID.String(),
		Aliases:    aliases,
		Names:      names,
		Tags:       tags,
	}, nil
}
