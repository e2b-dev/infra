//go:build linux

package factories

import (
	"context"
	"strings"

	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

const (
	orchestratorKind            ldcontext.Kind = "orchestrator"
	orchestratorCommitAttribute string         = "commit"
)

func orchestratorContextProvider(nodeID, commit string) featureflags.ContextProvider {
	versionContext := ldcontext.NewBuilder(nodeID).
		Kind(orchestratorKind).
		SetString(orchestratorCommitAttribute, commit).
		Build()

	return func(context.Context) ldcontext.Context {
		return versionContext
	}
}

// instanceGroupContextProvider keys flag rules on the managed instance group the
// node belongs to. The name is optional: an unset one yields an empty key, which
// is dropped before the multi-context is built. Trimming collapses whitespace to
// that same empty key, which a non-empty run of spaces would otherwise survive
// as a context matching no rule.
func instanceGroupContextProvider(instanceGroupName string) featureflags.ContextProvider {
	instanceGroupContext := featureflags.InstanceGroupContext(strings.TrimSpace(instanceGroupName))

	return func(context.Context) ldcontext.Context {
		return instanceGroupContext
	}
}
