package nfs

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"strconv"
	"testing"

	"cloud.google.com/go/storage"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willscott/go-nfs-client/nfs"
	"github.com/willscott/go-nfs-client/nfs/rpc"
	"github.com/zeldovich/go-rpcgen/rfc1057"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/nfs/gcs"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/portmap"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
)

func TestRoundTrip(t *testing.T) {
	t.Parallel()

	// setup data
	sandboxID := uuid.NewString()
	bucketName := "e2b-staging-joe-fc-build-cache"
	slog.SetLogLoggerLevel(slog.LevelDebug)

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

	// setup gcs client
	gcsClient, err := storage.NewGRPCClient(t.Context(), storage.WithDisabledClientMetrics())
	require.NoError(t, err)
	t.Cleanup(func() {
		err := gcsClient.Close()
		assert.NoError(t, err)
	})
	bucket := gcsClient.Bucket(bucketName)

	// setup nfs proxy server
	nfsConfig := net.ListenConfig{}
	nfsListener, err := nfsConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		err := nfsListener.Close()
		assert.NoError(t, err)
	})

	nfsProxy := NewProxy(t.Context(), sandboxes, bucket)
	go func() {
		err := nfsProxy.Serve(nfsListener)
		assert.NoError(t, err)
	}()

	// get nfs server's dynamic port
	nfsAddr := nfsListener.Addr().String()
	host, portText, err := net.SplitHostPort(nfsAddr)
	require.NoError(t, err)
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)

	// setup portmap server
	portmapConfig := net.ListenConfig{}
	pmListener, err := portmapConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		err := pmListener.Close()
		assert.NoError(t, err)
	})

	pm := portmap.NewPortMap(t.Context())
	pm.RegisterPort(t.Context(), uint32(port))
	go func() {
		err := pm.Serve(t.Context(), pmListener)
		assert.NoError(t, err)
	}()

	// connect via nfs client
	auth := rpc.NewAuthUnix("", 100, 101)

	// dial portmap server
	portmapperTCPClient, err := rpc.DialTCP("tcp", pmListener.Addr().String(), false)
	require.NoError(t, err)
	t.Cleanup(func() {
		portmapperTCPClient.Close()
	})
	portmapperClient := &rpc.Portmapper{Client: portmapperTCPClient}
	t.Cleanup(func() {
		portmapperClient.Close()
	})

	retrievedPort, err := portmapperClient.Getport(rpc.Mapping{
		Prog: nfs.Nfs3Prog,
		Vers: nfs.Nfs3Vers,
		Prot: rfc1057.IPPROTO_TCP,
	})
	require.NoError(t, err)

	nfsClient, err := nfs.DialServiceAtPort(host, retrievedPort)
	require.NoError(t, err)

	// request mount
	mount := &nfs.Mount{
		Client: nfsClient,
	}
	target, err := mount.Mount(".", auth.Auth())
	require.NoError(t, err)

	// write a file through nfs
	const perms = 0o642
	fp, err := target.OpenFile("/sandbox-id.txt", perms)
	require.NoError(t, err)
	data := []byte(sandboxID)
	n, err := fp.Write(data)
	require.NoError(t, err)
	assert.Equal(t, len(data), n)
	err = fp.Close()
	require.NoError(t, err)

	// verify file contents through gcs
	objectName := sandboxID + "/sandbox-id.txt"
	object := bucket.Object(objectName)

	// verify metadata
	attrs, err := object.Attrs(t.Context())
	require.NoErrorf(t, err, "failed to get object attrs for %s", objectName)
	assert.Equalf(t, map[string]string{
		gcs.MetadataPermsAttr: fmt.Sprintf("%03o", os.FileMode(perms)),
	}, attrs.Metadata, "wrong metadata for %s", objectName)

	// verify contents
	sandboxIDReader, err := bucket.Object(objectName).NewReader(t.Context())
	require.NoErrorf(t, err, "failed to read %s from bucket", objectName)
	data, err = io.ReadAll(sandboxIDReader)
	require.NoError(t, err)
	assert.Equal(t, sandboxID, string(data))

	// list file in nfs
	items, err := target.ReadDirPlus("/")
	require.NoError(t, err)
	require.Len(t, items, 1)
	item := items[0]
	assert.Equal(t, "sandbox-id.txt", item.Name())
	assert.Equal(t, perms, int(item.Mode()))

	// verify the file can be read
	fp, err = target.Open("sandbox-id.txt")
	require.NoError(t, err)
	buff := make([]byte, 1024) // way more bytes than we need
	n, err = fp.Read(buff)
	require.NoError(t, err)
	assert.Equal(t, len(sandboxID), n)
	assert.Equal(t, sandboxID, string(buff[:n]))
}
