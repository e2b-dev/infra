package base

import (
	"context"
	"fmt"
	"strconv"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/cache"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
)

func (bb *BaseBuilder) Hash(ctx context.Context, _ phases.LayerResult) (string, error) {
	var baseSource string
	if bb.Config.FromTemplate != nil {
		// When building from template, use the base template metadata
		baseSource = fmt.Sprintf("template:%s", bb.Config.FromTemplate.GetBuildID())
	} else {
		// Note: When "latest" tag is used, the cached version might become ambiguous (not always latest)
		// To update it now, you need to force the rebuild of the template, which will update this layer for all templates
		// in the team. This is okay for now, as the cache is not shared between teams, but it might need to be changed
		// when global caches are implemented.

		// When building from image, use the image name
		baseSource = bb.Config.FromImage
	}

	provisionVersion := provisionScriptFile
	if val, err := bb.featureFlags.IntFlag(
		ctx,
		featureflags.BuildProvisionVersion,
		featureflags.TemplateContext(bb.Config.TemplateID),
		featureflags.TeamContext(bb.Config.TeamID),
	// for dev environments (fallback value), use the provision script hash
	); val != featureflags.BuildProvisionVersion.Fallback() && err == nil {
		provisionVersion = strconv.FormatInt(int64(val), 10)
	}

	return cache.HashKeys(
		bb.index.Version(),
		provisionVersion,
		strconv.FormatInt(bb.Config.DiskSizeMB, 10),
		baseSource,
	), nil
}
