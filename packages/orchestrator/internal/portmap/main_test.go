package portmap

import (
	"testing"

	"github.com/stretchr/testify/assert"
	portmap "github.com/zeldovich/go-rpcgen/rfc1057"
)

func TestPortmapRetrieval(t *testing.T) {
	t.Parallel()

	h := newHandlers()
	h.PMAPPROC_SET(portmap.Mapping{
		Prog: 100003,
		Vers: 2,
		Prot: 1,
		Port: 2049,
	})

	result := h.PMAPPROC_GETPORT(portmap.Mapping{Prog: 100003, Vers: 2, Prot: 1})
	assert.Equal(t, uint32(2049), uint32(result))
}
