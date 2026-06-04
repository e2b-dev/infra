package machineinfo

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsCompatibleWith(t *testing.T) {
	t.Parallel()

	const (
		n2Model = "85"  // Intel Cascade Lake
		n4Model = "207" // Intel Emerald Rapids
	)

	tests := []struct {
		name  string
		build MachineInfo
		node  MachineInfo
		want  bool
	}{
		{
			name:  "exact match",
			build: MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "6", CPUModel: n2Model},
			node:  MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "6", CPUModel: n2Model},
			want:  true,
		},
		{
			name:  "architecture mismatch",
			build: MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "6", CPUModel: n2Model},
			node:  MachineInfo{CPUArchitecture: "aarch64", CPUFamily: "6", CPUModel: n2Model},
			want:  false,
		},
		{
			name:  "family mismatch",
			build: MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "6", CPUModel: n2Model},
			node:  MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "23", CPUModel: n2Model},
			want:  false,
		},
		{
			name:  "older build on newer node is compatible (n2 -> n4)",
			build: MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "6", CPUModel: n2Model},
			node:  MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "6", CPUModel: n4Model},
			want:  true,
		},
		{
			name:  "newer build on older node is incompatible (n4 -> n2)",
			build: MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "6", CPUModel: n4Model},
			node:  MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "6", CPUModel: n2Model},
			want:  false,
		},
		{
			name:  "unlisted model mismatch",
			build: MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "6", CPUModel: n2Model},
			node:  MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "6", CPUModel: "79"},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.build.IsCompatibleWith(tt.node))
		})
	}
}

func TestIsExactMatch(t *testing.T) {
	t.Parallel()

	const (
		n2Model = "85"  // Intel Cascade Lake
		n4Model = "207" // Intel Emerald Rapids
	)

	tests := []struct {
		name string
		a    MachineInfo
		b    MachineInfo
		want bool
	}{
		{
			name: "exact match",
			a:    MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "6", CPUModel: n2Model},
			b:    MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "6", CPUModel: n2Model},
			want: true,
		},
		{
			name: "cross-generation is not an exact match (n2 vs n4)",
			a:    MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "6", CPUModel: n2Model},
			b:    MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "6", CPUModel: n4Model},
			want: false,
		},
		{
			name: "architecture mismatch",
			a:    MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "6", CPUModel: n2Model},
			b:    MachineInfo{CPUArchitecture: "aarch64", CPUFamily: "6", CPUModel: n2Model},
			want: false,
		},
		{
			name: "family mismatch",
			a:    MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "6", CPUModel: n2Model},
			b:    MachineInfo{CPUArchitecture: "x86_64", CPUFamily: "23", CPUModel: n2Model},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, tt.a.IsExactMatch(tt.b))
		})
	}
}
