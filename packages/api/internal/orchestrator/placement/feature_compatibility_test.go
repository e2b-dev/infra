package placement

import (
	"strconv"
	"testing"

	"github.com/Masterminds/semver/v3"
	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

// Gates local to the tests: mutating featureGates would race the package's
// parallel tests.
var (
	testFeatureLow  = Feature{name: "low", minVersion: semver.New(0, 9, 0, "", "")}
	testFeatureHigh = Feature{name: "high", minVersion: semver.New(0, 11, 0, "", "")}
)

func gateOn(feature Feature, requested bool) featureGate {
	return featureGate{
		feature:   feature,
		requested: func(*orchestrator.SandboxCreateRequest) bool { return requested },
	}
}

func nodeAtVersion(id, version string) *nodemanager.Node {
	return nodemanager.NewTestNode(id, api.NodeStatusReady, 0, 8, nodemanager.WithOrchestratorVersion(version))
}

// TestFeatureGates_AreComplete holds every declared gate to a name, a floor and
// a predicate; a gate missing any of the three silently stops gating.
func TestFeatureGates_AreComplete(t *testing.T) {
	t.Parallel()

	for _, gate := range featureGates {
		assert.NotEmpty(t, gate.feature.name, "feature has no name")
		assert.NotNil(t, gate.feature.minVersion, "feature %q has no floor", gate.feature.name)
		assert.NotNil(t, gate.requested, "feature %q has no request predicate", gate.feature.name)
	}
}

func TestFeaturesFrom_TakesTheHighestFloor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		gates        []featureGate
		wantVersion  string
		wantFeatures []string
	}{
		{
			name:         "no gates",
			gates:        nil,
			wantVersion:  "",
			wantFeatures: []string{},
		},
		{
			name:         "gate not requested",
			gates:        []featureGate{gateOn(testFeatureHigh, false)},
			wantVersion:  "",
			wantFeatures: []string{},
		},
		{
			name:         "single gate",
			gates:        []featureGate{gateOn(testFeatureLow, true)},
			wantVersion:  "0.9.0",
			wantFeatures: []string{"low"},
		},
		{
			// Both orders land on the higher floor: a fold comparing only
			// against the previous entry passes one and fails the other.
			name:         "highest floor wins ascending",
			gates:        []featureGate{gateOn(testFeatureLow, true), gateOn(testFeatureHigh, true)},
			wantVersion:  "0.11.0",
			wantFeatures: []string{"low", "high"},
		},
		{
			name:         "highest floor wins descending",
			gates:        []featureGate{gateOn(testFeatureHigh, true), gateOn(testFeatureLow, true)},
			wantVersion:  "0.11.0",
			wantFeatures: []string{"high", "low"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			requirement := featuresFrom(testSbxRequest("sbx-1"), tt.gates)

			assert.Equal(t, tt.wantVersion, requirement.MinVersion())
			assert.Equal(t, tt.wantFeatures, requirement.FeatureNames())
		})
	}
}

func TestNodeSatisfiesFeatures(t *testing.T) {
	t.Parallel()

	requirement := featuresFrom(testSbxRequest("sbx-1"), []featureGate{gateOn(testFeatureHigh, true)})

	tests := []struct {
		name        string
		nodeVersion string
		requirement FeatureRequirement
		want        bool
	}{
		{
			name:        "no requirement accepts any node",
			nodeVersion: "0.1.0",
			requirement: FeatureRequirement{},
			want:        true,
		},
		{
			name:        "no requirement accepts an unreadable version",
			nodeVersion: "",
			requirement: FeatureRequirement{},
			want:        true,
		},
		{
			name:        "node at the floor",
			nodeVersion: "0.11.0",
			requirement: requirement,
			want:        true,
		},
		{
			name:        "node above the floor",
			nodeVersion: "0.12.3",
			requirement: requirement,
			want:        true,
		},
		{
			name:        "node below the floor",
			nodeVersion: "0.10.0",
			requirement: requirement,
			want:        false,
		},
		{
			// A distribution suffix marks a build of the release, not a step
			// before it; plain semver sorts it below the bare release.
			name:        "distribution suffix at the floor",
			nodeVersion: "0.11.0-ee",
			requirement: requirement,
			want:        true,
		},
		{
			name:        "distribution suffix below the floor",
			nodeVersion: "0.10.0-ee",
			requirement: requirement,
			want:        false,
		},
		{
			name:        "build metadata at the floor",
			nodeVersion: "0.11.0+abc123",
			requirement: requirement,
			want:        true,
		},
		{
			name:        "v prefix",
			nodeVersion: "v0.11.0",
			requirement: requirement,
			want:        true,
		},
		{
			name:        "unreadable version fails closed",
			nodeVersion: "not-a-version",
			requirement: requirement,
			want:        false,
		},
		{
			name:        "missing version fails closed",
			nodeVersion: "",
			requirement: requirement,
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, NodeSatisfiesFeatures(nodeAtVersion("node-1", tt.nodeVersion), tt.requirement))
		})
	}
}

func TestAnyNodeSatisfiesFeatures(t *testing.T) {
	t.Parallel()

	requirement := featuresFrom(testSbxRequest("sbx-1"), []featureGate{gateOn(testFeatureHigh, true)})

	tests := []struct {
		name        string
		versions    []string
		requirement FeatureRequirement
		want        bool
	}{
		{
			name:        "no requirement passes an empty fleet",
			versions:    nil,
			requirement: FeatureRequirement{},
			want:        true,
		},
		{
			name:        "requirement fails an empty fleet",
			versions:    nil,
			requirement: requirement,
			want:        false,
		},
		{
			name:        "whole fleet too old",
			versions:    []string{"0.10.0", "0.9.9", "0.10.0-ee"},
			requirement: requirement,
			want:        false,
		},
		{
			// One new node is enough: the check answers whether the fleet is
			// new enough, not whether a node is free.
			name:        "one node new enough",
			versions:    []string{"0.10.0", "0.11.0"},
			requirement: requirement,
			want:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			nodes := make([]*nodemanager.Node, 0, len(tt.versions))
			for i, version := range tt.versions {
				nodes = append(nodes, nodeAtVersion("node-"+strconv.Itoa(i), version))
			}

			assert.Equal(t, tt.want, anyNodeSatisfiesFeatures(nodes, tt.requirement))
		})
	}
}

// TestRequiredFeatures_HTTPSBackendPorts exercises the live gate against the
// request the orchestrator receives. It is the only test that would catch the
// predicate reading a field the create path does not populate.
func TestRequiredFeatures_HTTPSBackendPorts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		httpsPorts  []uint32
		wantVersion string
	}{
		{name: "no network config", httpsPorts: nil, wantVersion: ""},
		{name: "empty list", httpsPorts: []uint32{}, wantVersion: ""},
		{name: "one port", httpsPorts: []uint32{8443}, wantVersion: "0.10.0"},
		{name: "several ports", httpsPorts: []uint32{443, 8443}, wantVersion: "0.10.0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := testSbxRequest("sbx-1")
			if tt.httpsPorts != nil {
				req.Sandbox.Network = &orchestrator.SandboxNetworkConfig{
					Ingress: &orchestrator.SandboxNetworkIngressConfig{HttpsPorts: tt.httpsPorts},
				}
			}

			assert.Equal(t, tt.wantVersion, requiredFeatures(req).MinVersion())
		})
	}
}
