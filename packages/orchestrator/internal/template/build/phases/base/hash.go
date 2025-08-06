package base

import (
	"fmt"
	"strconv"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/cache"
)

func HashBase(index cache.Index, template config.TemplateConfig, provisionScriptFile string) string {
	var baseSource string
	if template.FromTemplate != nil {
		// When building from template, use the base template metadata
		baseSource = fmt.Sprintf("template:%s", template.FromTemplate.GetBuildID())
	} else {
		// Note: When "latest" tag is used, the cached version might become ambiguous (not always latest)
		// To update it now, you need to force the rebuild of the template, which will update this layer for all templates
		// in the team. This is okay for now, as the cache is not shared between teams, but it might need to be changed
		// when global caches are implemented.

		// When building from image, use the image name
		baseSource = template.FromImage
	}

	return cache.HashKeys(
		index.Version(),
		provisionScriptFile,
		strconv.FormatInt(template.DiskSizeMB, 10),
		baseSource,
	)
}
