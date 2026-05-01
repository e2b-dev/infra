package testutils

import (
	"context"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/nbd/nbdutil"
)

// TemplateRootfs re-exports nbdutil.TemplateRootfs for backward compatibility.
func TemplateRootfs(ctx context.Context, buildID string) (*nbdutil.BuildDevice, *nbdutil.Cleaner, error) {
	return nbdutil.TemplateRootfs(ctx, buildID)
}
