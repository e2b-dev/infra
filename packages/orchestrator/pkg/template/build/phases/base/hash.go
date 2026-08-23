//go:build linux

package base

import (
	"context"
	"fmt"
	"strconv"

	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/core/rootfs"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/phases"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/phases/base/distro"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/storage/cache"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (bb *BaseBuilder) Hash(ctx context.Context, _ phases.LayerResult) (string, error) {
	ctx, span := tracer.Start(ctx, "hash base")
	defer span.End()

	var baseSource string
	if bb.Config.FromTemplate != nil {
		// When building from template, use the base template metadata
		baseSource = fmt.Sprintf("template:%s", bb.Config.FromTemplate.GetBuildID())
	} else {
		// Note: When "default" tag is used, the cached version might become ambiguous (not always default)
		// To update it now, you need to force the rebuild of the template, which will update this layer for all templates
		// in the team. This is okay for now, as the cache is not shared between teams, but it might need to be changed
		// when global caches are implemented.

		// When building from image, use the image name
		baseSource = bb.Config.FromImage
	}

	// For fallback/dev environments, include baked rootfs file contents and
	// the distro provisioning contract (profiles + init blocks — the rendered
	// selector is part of the script but not of the raw provisionScriptFile
	// hashed here) in the provision version. In production,
	// BuildProvisionVersion controls rollout invalidation explicitly.
	provisionVersion := cache.HashKeys(provisionScriptFile, rootfs.FilesHash(), distro.Fingerprint())
	if val := bb.featureFlags.IntFlag(
		ctx,
		featureflags.BuildProvisionVersion,
		featureflags.TemplateContext(bb.Config.TemplateID),
		featureflags.TeamContext(bb.Config.TeamID),
		// for dev environments (fallback value), use the provision script hash
	); val != featureflags.BuildProvisionVersion.Fallback() {
		provisionVersion = strconv.FormatInt(int64(val), 10)
	}

	telemetry.SetAttributes(ctx,
		attribute.String("index_version", bb.index.Version()),
		attribute.String("provision_version", provisionVersion),
		attribute.String("base_source", baseSource),
		attribute.Int64("disk_size_mb", bb.Config.DiskSizeMB),
	)

	keys := []string{
		provisionVersion,
		strconv.FormatInt(bb.Config.DiskSizeMB, 10),
		baseSource,
	}

	// Only when there are arguments, and this is the whole reason the append is
	// conditional: HashKeys writes a separator before every key, so contributing an
	// empty string would change the digest of every cached base layer for every team,
	// including the ones that never touched this flag. Appending only when a team is
	// targeted keeps their keys byte-identical, and removes a team's variant by
	// returning them to the hash their existing layers are already stored under.
	//
	// Keyed on the arguments rather than the variant's name: the name is a label, two
	// names can carry the same arguments, and it is the arguments a build step can
	// observe. Sorted, because map iteration order is not stable and a cache key that
	// varies run to run caches nothing.
	if len(bb.Config.CmdlineArgs) > 0 {
		// KernelArgs.String() renders sorted, which is what makes this a stable key: Go
		// randomises map iteration, and a cache key that varies run to run caches nothing.
		keys = append(keys, "cmdline:"+fc.KernelArgs(bb.Config.CmdlineArgs).String())
	}

	return cache.HashKeys(bb.index.Version(), keys...), nil
}
