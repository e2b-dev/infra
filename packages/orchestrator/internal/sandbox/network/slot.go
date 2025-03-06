package network

import "fmt"

// We are using a more debuggable IP address allocation for now that only covers 255 addresses.
const (
	octetSize = 256
	octetMax  = octetSize - 1
	// This is the maximum number of IP addresses that can be allocated.
	slotsSize = octetSize * octetSize

	hostMask = 32
	vMask    = 30
	tapMask  = 30
)

type Slot struct {
	Key string
	Idx int
}

func NewSlot(key string, idx int) *Slot {
	return &Slot{
		Key: key,
		Idx: idx,
	}
}

func (s *Slot) VpeerName() string {
	return "eth0"
}

func (s *Slot) getOctets() (int, int) {
	rem := s.Idx % octetSize
	octet := (s.Idx - rem) / octetSize

	return octet, rem
}

func (s *Slot) VpeerIP() string {
	firstOctet, secondOctet := s.getOctets()

	return fmt.Sprintf("10.%d.%d.2", firstOctet, secondOctet)
}

func (s *Slot) VethIP() string {
	firstOctet, secondOctet := s.getOctets()

	return fmt.Sprintf("10.%d.%d.1", firstOctet, secondOctet)
}

func (s *Slot) VMask() int {
	return vMask
}

func (s *Slot) VethName() string {
	return fmt.Sprintf("veth-%d", s.Idx)
}

func (s *Slot) VethCIDR() string {
	return fmt.Sprintf("%s/%d", s.VethIP(), s.VMask())
}

func (s *Slot) VpeerCIDR() string {
	return fmt.Sprintf("%s/%d", s.VpeerIP(), s.VMask())
}

func (s *Slot) HostCIDR() string {
	return fmt.Sprintf("%s/%d", s.HostIP(), s.HostMask())
}

func (s *Slot) HostMask() int {
	return hostMask
}

// IP address for the sandbox from the host machine.
// You can use it to make requests to the sandbox.
func (s *Slot) HostIP() string {
	firstOctet, secondOctet := s.getOctets()

	return fmt.Sprintf("192.168.%d.%d", firstOctet, secondOctet)
}

func (s *Slot) NamespaceIP() string {
	return "169.254.0.21"
}

func (s *Slot) NamespaceID() string {
	return fmt.Sprintf("ns-%d", s.Idx)
}

func (s *Slot) TapName() string {
	return "tap0"
}

func (s *Slot) TapIP() string {
	return "169.254.0.22"
}

func (s *Slot) TapMask() int {
	return tapMask
}

func (s *Slot) TapCIDR() string {
	return fmt.Sprintf("%s/%d", s.TapIP(), s.TapMask())
}
