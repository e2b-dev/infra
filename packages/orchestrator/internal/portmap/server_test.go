package portmap

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/willscott/go-nfs-client/nfs"
	portmap "github.com/zeldovich/go-rpcgen/rfc1057"
)

func TestPortMapServer(t *testing.T) {
	t.Parallel()

	const fakeNfsPort = 19392

	listenConfig := net.ListenConfig{}
	lis, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		err := lis.Close()
		assert.NoError(t, err)
	})

	s := NewPortMap(t.Context())
	s.RegisterPort(t.Context(), fakeNfsPort)
	go func() {
		err := s.Serve(t.Context(), lis)
		assert.NoError(t, err)
	}()

	// make client
	a, err := net.ResolveTCPAddr("tcp", lis.Addr().String())
	require.NoError(t, err)
	conn, err := net.DialTCP("tcp", nil, a)
	require.NoError(t, err)
	client := portmap.MakeClient(conn, portmap.PMAP_PROG, portmap.PMAP_VERS)

	// get port
	cred := portmap.Opaque_auth{Flavor: portmap.AUTH_NONE}
	vfer := portmap.Opaque_auth{Flavor: portmap.AUTH_NONE}
	input := portmap.Mapping{
		Prog: nfs.Nfs3Prog,
		Vers: nfs.Nfs3Vers,
		Prot: portmap.IPPROTO_TCP,
		Port: 0,
	}
	var output portmap.Uint32
	err = client.Call(portmap.PMAPPROC_GETPORT, cred, vfer, &input, &output)
	require.NoError(t, err)

	// verify results
	assert.Equal(t, uint32(fakeNfsPort), uint32(output))
}
