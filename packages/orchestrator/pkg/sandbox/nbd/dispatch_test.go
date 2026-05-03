package nbd

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Regression: the old single-uint32 read of flags+type let any NBD_CMD_FLAG_*
// bit set by the kernel leak into Type and break dispatch.
func TestParseRequest_NonZeroFlagsDoNotCorruptType(t *testing.T) {
	t.Parallel()

	const nbdCmdFlagFUA uint16 = 1 << 0

	header := make([]byte, 28)
	binary.BigEndian.PutUint32(header[0:4], NBDRequestMagic)
	binary.BigEndian.PutUint16(header[4:6], nbdCmdFlagFUA)
	binary.BigEndian.PutUint16(header[6:8], NBDCmdWrite)

	req := parseRequest(header)

	assert.Equal(t, nbdCmdFlagFUA, req.Flags)
	assert.Equal(t, uint16(NBDCmdWrite), req.Type)
}
