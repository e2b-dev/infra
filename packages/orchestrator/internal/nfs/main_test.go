package nfs

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"cloud.google.com/go/storage"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willscott/go-nfs-client/nfs"
	"github.com/willscott/go-nfs-client/nfs/rpc"
	"github.com/zeldovich/go-rpcgen/rfc1057"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/nfs/gcs"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/portmap"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
)

func TestRoundTrip(t *testing.T) {
	t.Parallel()

	// setup logging
	logCfg := zap.NewDevelopmentConfig()
	logCfg.DisableStacktrace = true
	log, err := logCfg.Build(zap.AddStacktrace(zap.ErrorLevel))
	require.NoError(t, err)
	zap.ReplaceGlobals(log)

	// setup data
	sandboxID := uuid.NewString()
	teamID := uuid.NewString()
	bucketName := "e2b-staging-joe-fc-build-cache"
	volumeName := "shared-volume-1"

	slot := &network.Slot{Key: "abc", HostIP: net.IPv4(127, 0, 0, 1)}
	require.Equal(t, "127.0.0.1", slot.HostIP.String(), "required for the test to work")

	sandboxes := sandbox.NewSandboxesMap()
	sandboxes.Insert(&sandbox.Sandbox{
		Metadata: &sandbox.Metadata{
			Runtime: sandbox.RuntimeMetadata{
				SandboxID: sandboxID,
				TeamID:    teamID,
			},
		},
		Resources: &sandbox.Resources{
			Slot: slot,
		},
	})

	// setup gcs client
	gcsClient, err := storage.NewGRPCClient(t.Context(), storage.WithDisabledClientMetrics())
	if err != nil && strings.Contains(err.Error(), "could not find default credentials") {
		t.Skip("skipping test because no default credentials are available")
	}

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
	target, err := mount.Mount(volumeName, auth.Auth())
	require.NoError(t, err)

	t.Run("write file", func(t *testing.T) {
		t.Parallel()

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
		objectName := teamID + "/sandbox-id.txt"
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
	})

	t.Run("mkdir", func(t *testing.T) {
		t.Parallel()

		path := uuid.NewString()
		fh, err := target.Mkdir(path, 0o755)
		require.NoError(t, err)
		assert.NotNil(t, fh)
	})

	t.Run("list file in nfs", func(t *testing.T) {
		t.Parallel()

		// setup root dir, to prevent collisions
		path := uuid.NewString()
		mkdir(t, target, path, 0o755)

		// write files
		writeFile(t, target, filepath.Join(path, "file.txt"), "file.txt contents", 0o644)
		writeFile(t, target, filepath.Join(path, "file2.txt"), "file2.txt contents", 0o755)

		// ensure files can be listed
		items, err := target.ReadDirPlus(path)
		require.NoError(t, err)
		require.Len(t, items, 2)

		// normalize the order
		slices.SortFunc(items, func(a, b *nfs.EntryPlus) int {
			return strings.Compare(a.Name(), b.Name())
		})

		assert.Equal(t, filepath.Join(path, "file.txt"), items[0].Name())
		assert.Equal(t, os.FileMode(0o644), items[0].Mode())
		assert.Equal(t, filepath.Join(path, "file2.txt"), items[1].Name())
		assert.Equal(t, os.FileMode(0o755), items[1].Mode())
	})

	t.Run("access", func(t *testing.T) {
		t.Parallel()

		path := uuid.NewString()
		mkdir(t, target, path, 0o755)
		writeFile(t, target, filepath.Join(path, "file.txt"), "file.txt contents", 0o644)
		mode, err := target.Access(filepath.Join(path, "file.txt"), 0o644)
		require.NoError(t, err)
		assert.Equal(t, uint32(0o644), mode)
	})

	t.Run("lookup missing file", func(t *testing.T) {
		t.Parallel()

		// verify that file can be read with getattr
		path := uuid.NewString()
		stat1, fh1, err := target.Lookup(path)
		require.ErrorIs(t, err, os.ErrNotExist)
		assert.Nil(t, fh1)
		assert.Nil(t, stat1)
	})
}

func writeFile(t *testing.T, target *nfs.Target, path string, content string, perm os.FileMode) {
	t.Helper()

	fp, err := target.OpenFile(path, perm)
	require.NoError(t, err)

	n, err := fp.Write([]byte(content))
	require.NoError(t, err)
	assert.Equal(t, len(content), n, "wrong number of bytes written")

	err = fp.Close()
	require.NoError(t, err)
}

func mkdir(t *testing.T, target *nfs.Target, path string, perm os.FileMode) []byte {
	t.Helper()

	fh, err := target.Mkdir(path, perm)
	require.NoError(t, err)

	return fh
}
