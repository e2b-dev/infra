package handlers

import (
	"context"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/db/queries"
)

// templateRow is a row of a template list. A query that lists templates
// selects these columns, in this order, so that its generated row type
// converts to this one.
type templateRow = queries.ListDefaultTemplatesRow

// templateToAPI maps a template list row. Without usage, the template's usage
// fields (spawn and build counts, last spawn, creator) stay zero or empty: a
// caller sees them for its own templates only, and admins for default ones.
func templateToAPI(ctx context.Context, row templateRow, withUsage bool) api.Template {
	envdVersion := ""
	if row.BuildEnvdVersion != nil {
		envdVersion = *row.BuildEnvdVersion
	}

	diskMB := int64(0)
	if row.BuildTotalDiskSizeMb != nil {
		diskMB = *row.BuildTotalDiskSizeMb
	}

	template := api.Template{
		TemplateID:  row.TemplateID,
		BuildID:     row.BuildID.String(),
		CpuCount:    api.CPUCount(row.BuildVcpu),
		MemoryMB:    api.MemoryMB(row.BuildRamMb),
		DiskSizeMB:  api.DiskSizeMB(diskMB),
		Public:      row.Public,
		Aliases:     row.Aliases,
		Names:       row.Names,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
		BuildStatus: getCorrespondingTemplateBuildStatus(ctx, row.BuildStatus),
		EnvdVersion: envdVersion,
		IsDefault:   &row.IsDefault,
		Description: row.DefaultDescription,
	}

	if withUsage {
		template.LastSpawnedAt = row.LastSpawnedAt
		template.SpawnCount = row.SpawnCount
		template.BuildCount = row.BuildCount
		if row.CreatorID != nil {
			template.CreatedBy = &api.TeamUser{Id: *row.CreatorID}
		}
	}

	return template
}
