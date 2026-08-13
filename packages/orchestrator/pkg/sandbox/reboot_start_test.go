package sandbox

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
)

func TestRebootStartContextReplaysExactTemplateCommand(t *testing.T) {
	t.Parallel()

	cwd := "/workspace"
	meta := metadata.Template{
		Start: &metadata.Start{
			StartCmd: "unshare --pid --fork --mount-proc /init",
			Context: metadata.Context{
				User:    "root",
				WorkDir: &cwd,
				EnvVars: map[string]string{
					"MONAD_CREDENTIAL_BOOTSTRAP_REQUIRED": "1",
					"PATH":                                "/custom/bin",
				},
			},
		},
	}
	command, gotCwd, envs, user, ok := rebootStartContext(meta)

	require.True(t, ok)
	require.Equal(t, "unshare --pid --fork --mount-proc /init", command)
	require.Equal(t, &cwd, gotCwd)
	require.Equal(t, "root", user)
	require.Equal(t, "1", envs["MONAD_CREDENTIAL_BOOTSTRAP_REQUIRED"])
	require.Equal(t, "/custom/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", envs["PATH"])
	require.Equal(t, "/custom/bin", meta.Start.Context.EnvVars["PATH"])
}

func TestRebootStartContextSkipsTemplatesWithoutAStartCommand(t *testing.T) {
	t.Parallel()

	_, _, _, _, ok := rebootStartContext(metadata.Template{})

	require.False(t, ok)
}
