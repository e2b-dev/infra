package orchestrator

import (
	"testing"

	nomadapi "github.com/hashicorp/nomad/api"
	"github.com/stretchr/testify/assert"
)

func TestIsHealthy(t *testing.T) {
	testCases := map[string]struct {
		input    *nomadapi.AllocationListStub
		expected bool
	}{
		"nil input": {
			input:    nil,
			expected: false,
		},
		"status is nil": {
			input: &nomadapi.AllocationListStub{
				DeploymentStatus: nil,
			},
			expected: false,
		},
		"healthy is nil": {
			input: &nomadapi.AllocationListStub{
				DeploymentStatus: &nomadapi.AllocDeploymentStatus{
					Healthy: nil,
				},
			},
			expected: false,
		},
		"status is not healthy": {
			input: &nomadapi.AllocationListStub{
				DeploymentStatus: &nomadapi.AllocDeploymentStatus{
					Healthy: ptr(false),
				},
			},
			expected: false,
		},
		"status is healthy": {
			input: &nomadapi.AllocationListStub{
				DeploymentStatus: &nomadapi.AllocDeploymentStatus{
					Healthy: ptr(true),
				},
			},
			expected: true,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			actual := isHealthy(tc.input)
			assert.Equal(t, tc.expected, actual)
		})
	}
}

func ptr(b bool) *bool {
	return &b
}

func TestFindPortInAllocation(t *testing.T) {
	type expected struct {
		host string
		port int
		ok   bool
	}

	testCases := map[string]struct {
		allocation *nomadapi.AllocationListStub
		expected   expected
	}{
		"nil input": {
			allocation: nil,
			expected:   expected{host: "", port: 0, ok: false},
		},
		"nil allocated resources": {
			allocation: &nomadapi.AllocationListStub{AllocatedResources: nil},
			expected:   expected{host: "", port: 0, ok: false},
		},
		"task reserved label match": {
			allocation: &nomadapi.AllocationListStub{
				AllocatedResources: &nomadapi.AllocatedResources{
					Tasks: map[string]*nomadapi.AllocatedTaskResources{
						"task1": {
							Networks: []*nomadapi.NetworkResource{
								{
									IP: "10.0.0.1",
									ReservedPorts: []nomadapi.Port{
										{Label: "grpc", Value: 5555},
									},
								},
							},
						},
					},
					Shared: nomadapi.AllocatedSharedResources{
						Networks: []*nomadapi.NetworkResource{},
					},
				},
			},
			expected: expected{host: "10.0.0.1", port: 5555, ok: true},
		},
		"task reserved default port match": {
			allocation: &nomadapi.AllocationListStub{
				AllocatedResources: &nomadapi.AllocatedResources{
					Tasks: map[string]*nomadapi.AllocatedTaskResources{
						"task1": {
							Networks: []*nomadapi.NetworkResource{
								{
									IP: "10.0.0.2",
									ReservedPorts: []nomadapi.Port{
										{Label: "http", Value: 8080},
									},
								},
							},
						},
					},
					Shared: nomadapi.AllocatedSharedResources{
						Networks: []*nomadapi.NetworkResource{},
					},
				},
			},
			expected: expected{host: "10.0.0.2", port: 8080, ok: true},
		},
		"task dynamic label match": {
			allocation: &nomadapi.AllocationListStub{
				AllocatedResources: &nomadapi.AllocatedResources{
					Tasks: map[string]*nomadapi.AllocatedTaskResources{
						"task1": {
							Networks: []*nomadapi.NetworkResource{
								{
									IP: "10.0.0.3",
									DynamicPorts: []nomadapi.Port{
										{Label: "grpc", Value: 7000},
									},
								},
							},
						},
					},
					Shared: nomadapi.AllocatedSharedResources{
						Networks: []*nomadapi.NetworkResource{},
					},
				},
			},
			expected: expected{host: "10.0.0.3", port: 7000, ok: true},
		},
		"task dynamic default port match": {
			allocation: &nomadapi.AllocationListStub{
				AllocatedResources: &nomadapi.AllocatedResources{
					Tasks: map[string]*nomadapi.AllocatedTaskResources{
						"task1": {
							Networks: []*nomadapi.NetworkResource{
								{
									IP: "10.0.0.4",
									DynamicPorts: []nomadapi.Port{
										{Label: "other", Value: 8080},
									},
								},
							},
						},
					},
					Shared: nomadapi.AllocatedSharedResources{
						Networks: []*nomadapi.NetworkResource{},
					},
				},
			},
			expected: expected{host: "10.0.0.4", port: 8080, ok: true},
		},
		"shared reserved label match": {
			allocation: &nomadapi.AllocationListStub{
				AllocatedResources: &nomadapi.AllocatedResources{
					Tasks: map[string]*nomadapi.AllocatedTaskResources{},
					Shared: nomadapi.AllocatedSharedResources{
						Networks: []*nomadapi.NetworkResource{
							{
								IP: "10.0.0.5",
								ReservedPorts: []nomadapi.Port{
									{Label: "grpc", Value: 6000},
								},
							},
						},
					},
				},
			},
			expected: expected{host: "10.0.0.5", port: 6000, ok: true},
		},
		"shared dynamic label match": {
			allocation: &nomadapi.AllocationListStub{
				AllocatedResources: &nomadapi.AllocatedResources{
					Tasks: map[string]*nomadapi.AllocatedTaskResources{},
					Shared: nomadapi.AllocatedSharedResources{
						Networks: []*nomadapi.NetworkResource{
							{
								IP: "10.0.0.6",
								DynamicPorts: []nomadapi.Port{
									{Label: "grpc", Value: 6100},
								},
							},
						},
					},
				},
			},
			expected: expected{host: "10.0.0.6", port: 6100, ok: true},
		},
		"shared reserved default port match": {
			allocation: &nomadapi.AllocationListStub{
				AllocatedResources: &nomadapi.AllocatedResources{
					Tasks: map[string]*nomadapi.AllocatedTaskResources{},
					Shared: nomadapi.AllocatedSharedResources{
						Networks: []*nomadapi.NetworkResource{
							{
								IP: "10.0.0.7",
								ReservedPorts: []nomadapi.Port{
									{Label: "bad", Value: 8080},
								},
							},
						},
					},
				},
			},
			expected: expected{host: "10.0.0.7", port: 8080, ok: true},
		},
		"shared dynamic default port match": {
			allocation: &nomadapi.AllocationListStub{
				AllocatedResources: &nomadapi.AllocatedResources{
					Tasks: map[string]*nomadapi.AllocatedTaskResources{},
					Shared: nomadapi.AllocatedSharedResources{
						Networks: []*nomadapi.NetworkResource{
							{
								IP: "10.0.0.8",
								DynamicPorts: []nomadapi.Port{
									{Label: "bad", Value: 8080},
								},
							},
						},
					},
				},
			},
			expected: expected{host: "10.0.0.8", port: 8080, ok: true},
		},
		"no matches anywhere": {
			allocation: &nomadapi.AllocationListStub{
				AllocatedResources: &nomadapi.AllocatedResources{
					Tasks: map[string]*nomadapi.AllocatedTaskResources{
						"task1": {
							Networks: []*nomadapi.NetworkResource{
								{
									IP: "10.0.0.9",
									ReservedPorts: []nomadapi.Port{
										{Label: "http", Value: 9090},
									},
									DynamicPorts: []nomadapi.Port{
										{Label: "metrics", Value: 9091},
									},
								},
							},
						},
					},
					Shared: nomadapi.AllocatedSharedResources{
						Networks: []*nomadapi.NetworkResource{
							{
								IP: "10.0.0.10",
								ReservedPorts: []nomadapi.Port{
									{Label: "ssh", Value: 2222},
								},
								DynamicPorts: []nomadapi.Port{
									{Label: "http-alt", Value: 8081},
								},
							},
						},
					},
				},
			},
			expected: expected{host: "", port: 0, ok: false},
		},
	}

	for name, tc := range testCases {
		o := &Orchestrator{defaultPort: 8080, portLabel: "grpc"}

		t.Run(name, func(t *testing.T) {
			host, port, ok := o.findPortInAllocation(tc.allocation)
			assert.Equal(t, tc.expected.host, host)
			assert.Equal(t, tc.expected.port, port)
			assert.Equal(t, tc.expected.ok, ok)
		})
	}
}
