package frontend

// Exposes the block device and allows to read/write blocks of data from it.
type UnixDevice struct{}

func NewUnixDevice(socketPath string) *UnixDevice {
	return &UnixDevice{}
}
