package nfsproxy

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/log"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/portmap"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
)

const (
	defaultDockerHost = "172.17.0.1"
	portMapperPort    = 111
)

//go:embed e2e_test.sh
var e2eTestScript []byte

type testLogConsumer struct {
	t *testing.T
}

func (t testLogConsumer) Accept(l testcontainers.Log) {
	t.t.Logf("SCRIPT: %s", l.Content)
}

func getListener(t *testing.T, port int32) net.Listener {
	t.Helper()

	if port < 1024 && os.Geteuid() != 0 {
		t.Skip("skipping test because it requires root privileges")
	}

	nfsConfig := net.ListenConfig{}
	listener, err := nfsConfig.Listen(t.Context(), "tcp", fmt.Sprintf(":%d", port))
	require.NoError(t, err)

	t.Cleanup(func() {
		err := listener.Close()
		assert.NoError(t, err)
	})

	return listener
}

// GetLocalIP returns the first non-loopback local IPv4 address of the host.
func getLocalIP(t *testing.T) net.IP {
	t.Helper()

	addrs, err := net.InterfaceAddrs()
	require.NoError(t, err)

	for _, address := range addrs {
		// Check the address type and if it is not a loopback
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			// Check if the IP is an IPv4 address
			if ipnet.IP.To4() != nil {
				return ipnet.IP
			}
		}
	}

	t.Fatal("no non-loopback IPv4 address found")

	return nil
}

func TestIntegrationTest(t *testing.T) {
	t.Parallel()

	// setup logging
	logCfg := zap.NewDevelopmentConfig()
	logger, err := logCfg.Build(zap.AddStacktrace(zap.ErrorLevel))
	require.NoError(t, err)
	zap.ReplaceGlobals(logger)

	// setup data
	localIP := getLocalIP(t)
	slot := &network.Slot{Key: "abc", HostIP: localIP}

	sandboxID := uuid.NewString()
	teamID := uuid.NewString()
	volumeType := "volume-type-1"
	volumeName := "test-volume-1"
	sandboxes := sandbox.NewSandboxesMap()
	sandboxes.Insert(&sandbox.Sandbox{
		Metadata: &sandbox.Metadata{
			Runtime: sandbox.RuntimeMetadata{
				SandboxID: sandboxID,
				TeamID:    teamID,
			},
			Config: sandbox.Config{
				VolumeMounts: []sandbox.VolumeMountConfig{
					{Name: volumeName, Path: "/mnt/volume", Type: volumeType},
				},
			},
		},
		Resources: &sandbox.Resources{
			Slot: slot,
		},
	})

	// launch nfs proxy server
	nfsListener := getListener(t, 0)

	volumePath := t.TempDir()

	config := cfg.Config{
		PersistentVolumeMounts: map[string]string{
			volumeType: volumePath,
		},
	}

	s := NewProxy(t.Context(), sandboxes, config)
	go func() {
		err := s.Serve(nfsListener)
		assert.NoError(t, err)
	}()

	_, nfsListenPortStr, err := net.SplitHostPort(nfsListener.Addr().String())
	require.NoError(t, err)

	nfsListenPort, err := strconv.Atoi(nfsListenPortStr)
	require.NoError(t, err)

	// launch portmapper
	pmListener := getListener(t, portMapperPort)
	pm := portmap.NewPortMap(t.Context())
	go func() {
		err := pm.Serve(t.Context(), pmListener)
		assert.NoError(t, err)
	}()

	// this spawns a container, runs our script, then exits
	testCtr, err := testcontainers.Run(t.Context(), "ubuntu:24.04",
		testcontainers.WithHostConfigModifier(func(hostConfig *container.HostConfig) {
			hostConfig.NetworkMode = "host"
			hostConfig.Privileged = true
		}),
		testcontainers.WithHostPortAccess(nfsListenPort, portMapperPort),
		testcontainers.WithEnv(map[string]string{
			"NFS_HOST": nfsListener.Addr().String(),
		}),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			Reader:            bytes.NewBuffer(e2eTestScript),
			ContainerFilePath: "/e2e_test.sh",
			FileMode:          0o777,
		}),
		testcontainers.WithCmd("sleep", "infinity"),
		testcontainers.WithLogger(log.TestLogger(t)),
		testcontainers.WithLogConsumerConfig(&testcontainers.LogConsumerConfig{
			Opts: nil,
			Consumers: []testcontainers.LogConsumer{
				testLogConsumer{t},
			},
		}),
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		ctx := context.WithoutCancel(t.Context())
		err := testCtr.Terminate(ctx, testcontainers.StopTimeout(time.Second))
		assert.NoError(t, err)
	})

	// run the actual test
	code, out, err := testCtr.Exec(t.Context(),
		[]string{"/e2e_test.sh"},
		exec.WithEnv([]string{
			"NFS_HOST=" + defaultDockerHost,
			"NFS_PORT=" + nfsListenPortStr,
			"NFS_VOLUME=" + volumeName,
		}),
	)
	require.NoError(t, err)
	output, err := io.ReadAll(out)
	require.NoError(t, err)
	assert.Equal(t, 0, code, string(output))
}
