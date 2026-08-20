//go:build linux

package finalize

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/buildcontext"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/phases"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// The finalize stamp is what makes a template's metadata describe the kernel that template
// actually booted. It starts from the source layer's metadata, which for a build started
// FROM another template is the parent's — so the stamp has to assign in both directions.
//
// Leaving it alone when this build resolved to no arguments was a real bug: a child whose
// team is not targeted kept its parent's arguments in metadata while its kernel booted
// without them, and a later filesystem-only cold boot replayed the parent's arguments into
// a guest that was never built with them.
func TestLayerStampsThisBuildsCmdlineArgs(t *testing.T) {
	t.Parallel()

	parentArgs := map[string]string{"psi": "1"}

	tests := []struct {
		name    string
		inherit map[string]string
		build   map[string]string
		want    map[string]string
	}{
		{
			name:    "this build's parameters win over inherited ones",
			inherit: parentArgs,
			build:   map[string]string{"nokaslr": ""},
			want:    map[string]string{"nokaslr": ""},
		},
		{
			// The regression: no arguments must CLEAR an inherited variant, not defer to it.
			name:    "no parameters clears inherited ones",
			inherit: parentArgs,
			build:   nil,
			want:    nil,
		},
		{
			name:    "nothing inherited and nothing set stays empty",
			inherit: nil,
			build:   nil,
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ppb := &PostProcessingBuilder{
				BuildContext: buildcontext.BuildContext{
					Config:   config.TemplateConfig{CmdlineArgs: tt.build},
					Template: storage.Paths{BuildID: "build-1"},
				},
			}

			got, err := ppb.Layer(
				t.Context(),
				phases.LayerResult{Metadata: metadata.Template{CmdlineArgs: tt.inherit}},
				"hash",
			)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got.Metadata.CmdlineArgs)
		})
	}
}
