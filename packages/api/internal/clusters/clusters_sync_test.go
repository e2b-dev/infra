package clusters

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/db/queries"
)

func TestClusterConfigMatches(t *testing.T) {
	t.Parallel()

	domain := "example.com"
	authOrgID := "org-a"
	source := queries.Cluster{
		ID:                 uuid.New(),
		Endpoint:           "api.example.com:443",
		EndpointTls:        true,
		Token:              "secret-a",
		SandboxProxyDomain: &domain,
		AuthOrgID:          &authOrgID,
	}
	cluster := &Cluster{
		ID: source.ID,
		remoteConfig: &remoteClusterConfig{
			endpoint:      source.Endpoint,
			endpointTLS:   source.EndpointTls,
			token:         source.Token,
			sandboxDomain: source.SandboxProxyDomain,
			authOrgID:     authOrgID,
		},
	}

	require.True(t, clusterConfigMatches(cluster, source))

	changed := source
	changed.Endpoint = "api.rotated.example.com:443"
	assert.False(t, clusterConfigMatches(cluster, changed))
	changed = source
	changed.Token = "secret-b"
	assert.False(t, clusterConfigMatches(cluster, changed))
	changed = source
	changed.SandboxProxyDomain = nil
	assert.False(t, clusterConfigMatches(cluster, changed))
}
