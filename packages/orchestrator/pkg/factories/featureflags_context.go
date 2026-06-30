//go:build linux

package factories

import (
	"context"

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
