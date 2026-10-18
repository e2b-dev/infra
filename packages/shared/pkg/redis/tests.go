package redis_utils

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.segfaultmedaddy.com/redistest"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// EnvTestDatabaseURL names the environment variable carrying the redis:// URL
// of the Redis server the tests share.
const EnvTestDatabaseURL = "REDIS_TEST_DATABASE_URL"

// testFactory is built by the first test that asks for a client, so a binary
// that never does needs no server.
var testFactory = sync.OnceValue(func() *redistest.RedisFactory {
	opts := utils.Must(redis.ParseURL(os.Getenv(EnvTestDatabaseURL)))

	return utils.Must(redistest.NewFactory(redistest.WithOptions(&redis.UniversalOptions{
		Addrs:     []string{opts.Addr},
		DB:        opts.DB,
		Username:  opts.Username,
		Password:  opts.Password,
		TLSConfig: opts.TLSConfig,
	})))
})

// SetupInstance returns a client on the server EnvTestDatabaseURL names whose
// keys are namespaced to the test, so parallel tests share the server without
// seeing each other's keys. The keys are removed and the client closed when
// the test ends.
//
// Commands that carry no key are sent unchanged and are not isolated: SCAN,
// KEYS, FLUSHDB and pub/sub. A test that depends on them uses
// SetupContainerInstance.
func SetupInstance(t *testing.T) redis.UniversalClient {
	t.Helper()

	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	client, _ := testFactory().Client(t)

	return client
}

// SetupContainerInstance starts a Redis container of the test's own, for tests
// whose commands SetupInstance cannot isolate.
func SetupContainerInstance(t *testing.T) redis.UniversalClient {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:        "redis:8-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForLog("Ready to accept connections"),
	}

	container, err := testcontainers.GenericContainer(t.Context(), testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		err := container.Terminate(context.WithoutCancel(t.Context()))
		assert.NoError(t, err)
	})

	host, err := container.Host(t.Context())
	require.NoError(t, err)

	port, err := container.MappedPort(t.Context(), "6379")
	require.NoError(t, err)

	client := redis.NewClient(&redis.Options{
		Addr: host + ":" + port.Port(),
	})

	t.Cleanup(func() {
		_ = client.Close()
	})

	return client
}
