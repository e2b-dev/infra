//go:build linux

package sandbox

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
)

// Pins the reboot safety gate to the dispatch rule (artifact OR request): the
// two decided independently once, and a memory-snapshot rescue passed dispatch
// only to be refused here.
func TestRebootAllowed_ArtifactOrRequest(t *testing.T) {
	t.Parallel()

	assert.True(t, rebootAllowed(metadata.Template{FilesystemOnly: true}, false))
	assert.True(t, rebootAllowed(metadata.Template{FilesystemOnly: true}, true))
	assert.True(t, rebootAllowed(metadata.Template{}, true), "explicit request cold-boots a memory snapshot")
	assert.False(t, rebootAllowed(metadata.Template{}, false), "memory snapshot without a demand keeps refusing")
}
