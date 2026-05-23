//go:build linux

package main

import (
	"context"
	"os"
	"strconv"

	"github.com/launchdarkly/go-sdk-common/v3/ldvalue"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/factories"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/tcpfirewall"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/version"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
)

var commitSHA string

func main() {
	applyTestFlagOverrides()

	factories.Run(factories.Options{
		Version:       version.Version,
		CommitSHA:     commitSHA,
		EgressFactory: defaultEgressFactory,
	})
}

func applyTestFlagOverrides() {
	if enabled, _ := strconv.ParseBool(os.Getenv("TESTS_USE_MEMFD")); enabled {
		featureflags.OverrideBoolFlag(featureflags.UseMemFdFlag, true)
	}
	if enabled, _ := strconv.ParseBool(os.Getenv("TESTS_MEMFILE_DIFF_DEDUP")); enabled {
		bestEffort, _ := strconv.ParseBool(os.Getenv("TESTS_MEMFILE_DIFF_DEDUP_BEST_EFFORT"))
		directIO, _ := strconv.ParseBool(os.Getenv("TESTS_MEMFILE_DIFF_DEDUP_DIRECT_IO"))
		featureflags.OverrideJSONFlag(featureflags.MemfileDiffDedupFlag, ldvalue.FromJSONMarshal(map[string]any{
			"enabled":    true,
			"bestEffort": bestEffort,
			"directIO":   directIO,
		}))
	}
}

func defaultEgressFactory(_ context.Context, deps *factories.Deps) (*factories.EgressSetup, error) {
	fw := tcpfirewall.New(
		deps.Logger,
		deps.Config.NetworkConfig,
		deps.Sandboxes,
		deps.MeterProvider,
		deps.FeatureFlags,
	)

	return &factories.EgressSetup{
		Proxy: fw,
		Start: fw.Start,
		Close: fw.Close,
	}, nil
}
