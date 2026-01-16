package nfs

import (
	"context"
	"net"
	"strconv"
	"testing"

	"cloud.google.com/go/storage"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willscott/go-nfs-client/nfs"
	"github.com/willscott/go-nfs-client/nfs/rpc"
)

func TestRoundTrip(t *testing.T) {
	t.Setenv("SANDBOXES_HOST_NETWORK_CIDR", "127.0.0.1")

	sandboxID := uuid.NewString()

	slot := &network.Slot{Key: "abc", HostIP: net.IPv4(127, 0, 0, 1)}
	require.Equal(t, "127.0.0.1", slot.HostIP.String(), "required for the test to work")

	sandboxes := sandbox.NewSandboxesMap()
	sandboxes.Insert(&sandbox.Sandbox{
		Metadata: &sandbox.Metadata{
			Runtime: sandbox.RuntimeMetadata{
				SandboxID: sandboxID,
			},
		},
		Resources: &sandbox.Resources{
			Slot: slot,
		},
	})

	s := NewProxy(sandboxes)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	gcsClient, err := storage.NewGRPCClient(t.Context(), storage.WithDisabledClientMetrics())
	require.NoError(t, err)

	go func() {
		err := s.Start(t.Context(), lis, gcsClient)
		assert.NoError(t, err)
	}()

	t.Cleanup(func() {
		ctx := context.WithoutCancel(t.Context())
		assert.NoError(t, s.Stop(ctx))
	})

	auth := rpc.NewAuthUnix("", 100, 101)

	nfsAddr := lis.Addr().String()
	host, portText, err := net.SplitHostPort(nfsAddr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)

	nfsClient, err := nfs.DialServiceAtPort(host, port)
	require.NoError(t, err)

	mount := &nfs.Mount{
		Client: nfsClient,
	}
	target, err := mount.Mount(".", auth.Auth())
	require.NoError(t, err)

	fp, err := target.OpenFile("/sandbox-id.txt", 0o666)
	require.NoError(t, err)
	data := []byte(sandboxID)
	n, err := fp.Write(data)
	require.NoError(t, err)
	assert.Equal(t, len(data), n)
	err = fp.Close()
	require.NoError(t, err)

	items, err := target.ReadDirPlus("/")
	require.NoError(t, err)
	require.Len(t, items, 1)

	item := items[0]
	assert.Equal(t, "sandbox-id.txt", item.Name())

	fp, err = target.Open("sandbox-id.txt")
	require.NoError(t, err)
	buff := make([]byte, 1024) // way more bytes than we need
	read, err := fp.Read(buff)
	assert.Equal(t, len(sandboxID), read)
	assert.Equal(t, sandboxID, string(buff))
}
