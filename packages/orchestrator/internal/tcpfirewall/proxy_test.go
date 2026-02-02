package tcpfirewall

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func dial(ctx context.Context, addr string) (net.Conn, error) {
	d := net.Dialer{}

	return d.DialContext(ctx, "tcp", addr)
}

// TestResilientListener_EMFILEWithContainer tests FD exhaustion using testcontainers.
// It runs a Python server with resilient accept (similar to resilientListener) inside
// a container with limited FDs. The server retries on EMFILE and logs when it happens.
// The test verifies that EMFILE was actually encountered via container logs.
func TestResilientListener_EMFILEWithContainer(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Second)
	defer cancel()

	// Python server that mimics resilientListener behavior:
	// - Accepts connections in a loop
	// - On EMFILE, logs and retries after a delay
	// - All connections eventually succeed
	const containerPort = "9999/tcp"
	const fdLimit int64 = 20

	pythonScript := `
import socket
import errno
import time
import resource

# Print FD limit
soft, hard = resource.getrlimit(resource.RLIMIT_NOFILE)
print(f'FD_LIMIT: soft={soft} hard={hard}', flush=True)

server = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
server.bind(('0.0.0.0', 9999))
server.listen(100)
print('SERVER_READY', flush=True)

connections = []
emfile_count = 0
accept_count = 0

while True:
    try:
        conn, addr = server.accept()
        accept_count += 1
        connections.append(conn)
        print(f'ACCEPTED: count={accept_count} active={len(connections)}', flush=True)
    except OSError as e:
        if e.errno == errno.EMFILE or e.errno == errno.ENFILE:
            emfile_count += 1
            print(f'EMFILE_ERROR: count={emfile_count} active={len(connections)}', flush=True)
            # Close oldest connections to free FDs (simulating cleanup)
            if len(connections) > 5:
                for _ in range(3):
                    if connections:
                        connections.pop(0).close()
            time.sleep(0.1)  # Retry delay like resilientListener
        else:
            print(f'OTHER_ERROR: {e}', flush=True)
            time.sleep(0.1)
`

	req := testcontainers.ContainerRequest{
		Image: "python:3.12-alpine",
		Cmd: []string{
			"python", "-u", "-c", pythonScript,
		},
		ExposedPorts: []string{containerPort},
		WaitingFor:   wait.ForLog("SERVER_READY").WithStartupTimeout(60 * time.Second),
		HostConfigModifier: func(hc *container.HostConfig) {
			hc.Ulimits = []*container.Ulimit{
				{
					Name: "nofile",
					Soft: fdLimit,
					Hard: fdLimit,
				},
			}
		},
	}

	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err, "Failed to start container")

	// Get container logs at the end
	defer func() {
		logs, logsErr := ctr.Logs(ctx)
		if logsErr == nil {
			logBytes, _ := io.ReadAll(logs)
			t.Logf("Container logs:\n%s", string(logBytes))
			logs.Close()
		}
		if termErr := ctr.Terminate(ctx); termErr != nil {
			t.Logf("Warning: failed to terminate container: %v", termErr)
		}
	}()

	// Get the mapped port
	mappedPort, err := ctr.MappedPort(ctx, "9999/tcp")
	require.NoError(t, err, "Failed to get mapped port")

	host, err := ctr.Host(ctx)
	require.NoError(t, err, "Failed to get container host")

	addr := net.JoinHostPort(host, mappedPort.Port())

	// Wait for server to be ready
	time.Sleep(500 * time.Millisecond)

	// Open many connections to trigger FD exhaustion
	var openConns []net.Conn
	defer func() {
		for _, c := range openConns {
			c.Close()
		}
	}()

	// Open connections - all should eventually succeed due to retry logic
	const numConnections = 30
	for i := range numConnections {
		dialCtx, dialCancel := context.WithTimeout(ctx, 5*time.Second)
		conn, dialErr := dial(dialCtx, addr)
		dialCancel()

		require.NoError(t, dialErr, "Connection %d should succeed (server retries on EMFILE)", i)
		openConns = append(openConns, conn)

		// Small delay between connections
		time.Sleep(50 * time.Millisecond)
	}

	t.Logf("Successfully opened %d connections", len(openConns))

	// Close connections
	for _, c := range openConns {
		c.Close()
	}
	openConns = nil

	// Give container time to log final state
	time.Sleep(500 * time.Millisecond)

	// Check container logs for EMFILE errors
	logs, err := ctr.Logs(ctx)
	require.NoError(t, err, "Failed to get container logs")
	logBytes, err := io.ReadAll(logs)
	require.NoError(t, err, "Failed to read container logs")
	logs.Close()

	logContent := string(logBytes)

	// Verify FD limit was applied
	require.Contains(t, logContent, fmt.Sprintf("FD_LIMIT: soft=%d", fdLimit),
		"Container should have FD limit applied")

	// Verify EMFILE was encountered
	require.Contains(t, logContent, "EMFILE_ERROR",
		"Should have encountered EMFILE errors - FD exhaustion was not triggered")

	// Count EMFILE occurrences
	emfileCount := strings.Count(logContent, "EMFILE_ERROR")
	t.Logf("Container encountered %d EMFILE errors and handled them with retry", emfileCount)

	require.Positive(t, emfileCount, "Should have logged at least one EMFILE error")
}

func TestIsTransientAcceptError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"EMFILE", syscall.EMFILE, true},
		{"ENFILE", syscall.ENFILE, true},
		{"EAGAIN", syscall.EAGAIN, true},
		{"ECONNABORTED", syscall.ECONNABORTED, true},
		{"wrapped EMFILE", &net.OpError{Op: "accept", Err: syscall.EMFILE}, true},
		{"wrapped ENFILE", &net.OpError{Op: "accept", Err: syscall.ENFILE}, true},
		{"wrapped ECONNABORTED", &net.OpError{Op: "accept", Err: syscall.ECONNABORTED}, true},
		{"ECONNRESET", syscall.ECONNRESET, false},
		{"generic error", errors.New("some error"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isTransientAcceptError(tt.err)
			assert.Equal(t, tt.want, got)
		})
	}
}
