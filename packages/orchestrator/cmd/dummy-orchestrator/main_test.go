package main

import (
	"testing"

	"github.com/stretchr/testify/require"

	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

func TestInitialServiceStatus(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		value  string
		status orchestratorinfo.ServiceInfoStatus
	}{
		"healthy by default": {value: "false", status: orchestratorinfo.ServiceInfoStatus_Healthy},
		"standby enabled":    {value: "true", status: orchestratorinfo.ServiceInfoStatus_Standby},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := initialServiceStatus(test.value)
			require.NoError(t, err)
			require.Equal(t, test.status, got)
		})
	}

	_, err := initialServiceStatus("not-a-bool")
	require.Error(t, err)
}
