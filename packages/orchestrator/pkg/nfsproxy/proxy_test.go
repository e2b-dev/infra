package nfsproxy

import (
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willscott/go-nfs-client/nfs"
	"github.com/willscott/go-nfs-client/nfs/rpc"
	"github.com/zeldovich/go-rpcgen/rfc1057"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/chrooted"
nfscfg "github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/nfsproxy/quota"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/portmap"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/network"
)

func createVolumeDir(t *testing.T, builder *chrooted.Builder, volumeType string, teamID, volumeID uuid.UUID) {
	t.Helper()

	fullVolumePath, err := builder.BuildVolumePath(volumeType, teamID, volumeID)
	require.NoError(t, err)

	t.Logf("creating volume dir: %s", fullVolumePath)
	err = os.MkdirAll(fullVolumePath, 0o755)
	require.NoError(t, err)
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()

	if syscall.Geteuid() != 0 {
		t.Skip("skipping test as it requires root privileges")
	}

	// setup logging
	logCfg := zap.NewDevelopmentConfig()
	logCfg.DisableStacktrace = true
	log, err := logCfg.Build(zap.AddStacktrace(zap.ErrorLevel))
	require.NoError(t, err)
	zap.ReplaceGlobals(log)

	// setup data
	sandboxID := uuid.NewString()
	teamID := uuid.New()
	volPath1 := t.TempDir()
	volType1 := "volume-type-1"
	volID1 := uuid.New()
	volName1 := "volume-1"
	volID2 := uuid.New()
	volName2 := "volume-2"
	volType2 := "volume-type-2"

	// set up paths
	config := cfg.Config{
		PersistentVolumeMounts: map[string]string{
			volType1: volPath1,
		},
	}

	builder := chrooted.NewBuilder(config)

	createVolumeDir(t, builder, volType1, teamID, volID1)
	createVolumeDir(t, builder, volType1, teamID, volID2)

	slot := &network.Slot{Key: "abc", HostIP: net.IPv4(127, 0, 0, 1)}
	require.Equal(t, "127.0.0.1", slot.HostIP.String(), "required for the test to work")

	sandboxes := sandbox.NewSandboxesMap()
	sandboxes.Insert(t.Context(), &sandbox.Sandbox{
		Metadata: &sandbox.Metadata{
			Config: sandbox.NewConfig(sandbox.Config{
				VolumeMounts: []sandbox.VolumeMountConfig{
					{ID: volID1, Name: volName1, Path: "/mnt/vol1", Type: volType1},
					{ID: volID2, Name: volName2, Path: "/mnt/vol2", Type: volType2},
				},
			}),
			Runtime: sandbox.RuntimeMetadata{
				SandboxID: sandboxID,
				TeamID:    teamID.String(),
			},
		},
		Resources: &sandbox.Resources{
			Slot: slot,
		},
	})

	// setup nfs proxy server
	nfsConfig := net.ListenConfig{}
	nfsListener, err := nfsConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		err := nfsListener.Close()
		assert.NoError(t, err)
	})

	nfsProxy, err := NewProxy(t.Context(), builder, sandboxes, nil, nfscfg.Config{})
	require.NoError(t, err)
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
	target, err := mount.Mount("/"+volName1, auth.Auth())
	require.NoError(t, err)

	t.Run("write file", func(t *testing.T) {
		t.Parallel()

		// write a file through nfs
		const perms os.FileMode = 0o642
		fp, err := target.OpenFile("/sandbox-id.txt", perms)
		require.NoError(t, err)
		data := []byte(sandboxID)
		n, err := fp.Write(data)
		require.NoError(t, err)
		assert.Equal(t, len(data), n)
		err = fp.Close()
		require.NoError(t, err)

		// verify file contents directly
		objectName := filepath.Join(volPath1, "team-"+teamID.String(), "vol-"+volID1.String(), "sandbox-id.txt")
		object, err := os.Open(objectName)
		require.NoError(t, err)

		// verify metadata
		stat, err := object.Stat()
		require.NoError(t, err)
		assert.Equal(t, perms, stat.Mode().Perm(), "wrong permissions for %s", objectName)

		// verify contents
		data, err = io.ReadAll(object)
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

		assert.Equal(t, "file.txt", items[0].Name())
		assert.Equal(t, os.FileMode(0o644), items[0].Mode())
		assert.Equal(t, "file2.txt", items[1].Name())
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

func TestQuotaEnforcement(t *testing.T) {
	t.Parallel()

	if syscall.Geteuid() != 0 {
		t.Skip("skipping test as it requires root privileges")
	}

	// setup logging
	logCfg := zap.NewDevelopmentConfig()
	logCfg.DisableStacktrace = true
	logger, err := logCfg.Build(zap.AddStacktrace(zap.ErrorLevel))
	require.NoError(t, err)
	zap.ReplaceGlobals(logger)

	// setup miniredis for quota tracking
	redisServer := miniredis.NewMiniRedis()
	err = redisServer.Start()
	require.NoError(t, err)
	t.Cleanup(func() {
		redisServer.Close()
	})

	redisClient := redis.NewClient(&redis.Options{
		Addr: redisServer.Addr(),
	})
	t.Cleanup(func() {
		err := redisClient.Close()
		assert.NoError(t, err)
	})

	// create quota tracker with short cache refresh for testing
	tracker := quota.NewTracker(redisClient, logger)
	tracker.SetUsageCacheRefresh(10 * time.Millisecond)

	// setup data
	const quotaBytes int64 = 10 * 1024 * 1024 // 10 MB
	sandboxID := uuid.NewString()
	teamID := uuid.New()
	volPath := t.TempDir()
	volType := "volume-type-quota"
	volID := uuid.New()
	volName := "quota-volume"

	// set up paths
	config := cfg.Config{
		PersistentVolumeMounts: map[string]string{
			volType: volPath,
		},
	}

	builder := chrooted.NewBuilder(config)
	createVolumeDir(t, builder, volType, teamID, volID)

	slot := &network.Slot{Key: "quota-test", HostIP: net.IPv4(127, 0, 0, 1)}

	sandboxes := sandbox.NewSandboxesMap()
	sandboxes.Insert(t.Context(), &sandbox.Sandbox{
		Metadata: &sandbox.Metadata{
			Config: sandbox.NewConfig(sandbox.Config{
				VolumeMounts: []sandbox.VolumeMountConfig{
					{ID: volID, Name: volName, Path: "/mnt/quota", Type: volType, Quota: quotaBytes},
				},
			}),
			Runtime: sandbox.RuntimeMetadata{
				SandboxID: sandboxID,
				TeamID:    teamID.String(),
			},
		},
		Resources: &sandbox.Resources{
			Slot: slot,
		},
	})

	// setup nfs proxy server with quota tracker
	nfsConfig := net.ListenConfig{}
	nfsListener, err := nfsConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		err := nfsListener.Close()
		assert.NoError(t, err)
	})

	nfsProxy, err := NewProxy(t.Context(), builder, sandboxes, tracker, nfscfg.Config{})
	require.NoError(t, err)
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

	// mount the volume
	mount := &nfs.Mount{
		Client: nfsClient,
	}
	target, err := mount.Mount("/"+volName, auth.Auth())
	require.NoError(t, err)

	// Step 1: Write initial file (should succeed - quota not exceeded yet)
	t.Log("Writing initial file (should succeed)")
	fp, err := target.OpenFile("/initial.txt", 0o644)
	require.NoError(t, err, "initial write should succeed")
	_, err = fp.Write([]byte("initial content"))
	require.NoError(t, err, "initial write should succeed")
	err = fp.Close()
	require.NoError(t, err)

	// Step 2: Set usage to exactly the quota limit via the tracker
	vol := quota.VolumeInfo{
		TeamID:   teamID,
		VolumeID: volID,
		Quota:    quotaBytes,
	}
	t.Log("Setting usage to quota limit (10 MB)")
	err = tracker.SetUsage(t.Context(), vol, quotaBytes)
	require.NoError(t, err)

	// Step 3: Expire the cache to force refresh from Redis
	tracker.ExpireCacheForTesting()

	// Step 4: Attempt to write - should be blocked
	t.Log("Attempting write after quota exceeded (should fail)")
	fp2, err := target.OpenFile("/blocked.txt", 0o644)
	// OpenFile with write flags should fail when quota is exceeded
	require.Error(t, err, "write should be blocked after quota exceeded")
	// Check for NFS3ERR_DQUOT (error code 69)
	nfsErr, ok := err.(*nfs.Error)
	require.True(t, ok, "error should be an NFS error, got: %T", err)
	assert.Equal(t, uint32(nfs.NFS3ErrDQuot), nfsErr.ErrorNum, "error should be NFS3ERR_DQUOT")
	assert.Nil(t, fp2)

	// Step 5: Attempt to read - should succeed
	t.Log("Attempting read after quota exceeded (should succeed)")
	readFp, err := target.Open("/initial.txt")
	require.NoError(t, err, "read should succeed even when quota exceeded")
	content, err := io.ReadAll(readFp)
	require.NoError(t, err, "reading file should succeed")
	assert.Equal(t, "initial content", string(content))
	err = readFp.Close()
	require.NoError(t, err)

	// Step 6: Verify stat also works (reads are not blocked)
	t.Log("Attempting stat after quota exceeded (should succeed)")
	_, _, err = target.Lookup("/initial.txt")
	require.NoError(t, err, "stat should succeed even when quota exceeded")

	// Step 7: Verify ReadDirPlus works (reads are not blocked)
	t.Log("Attempting ReadDirPlus after quota exceeded (should succeed)")
	entries, err := target.ReadDirPlus("/")
	require.NoError(t, err, "ReadDirPlus should succeed even when quota exceeded")
	assert.NotEmpty(t, entries, "should see at least the initial.txt file")
}
