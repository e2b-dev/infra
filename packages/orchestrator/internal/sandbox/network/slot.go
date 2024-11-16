package network

import (
	"fmt"
	"math/rand"
	"slices"

	consulApi "github.com/hashicorp/consul/api"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/consul"
)

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

func NewSlot() (*Slot, error) {
	kv := consul.Client.KV()

	var slot *Slot

	trySlot := func(slotIdx int, key string) (*Slot, error) {
		status, _, err := kv.CAS(&consulApi.KVPair{
			Key:         key,
			ModifyIndex: 0,
		}, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to write to Consul KV: %w", err)
		}

		if status {
			return &Slot{
				Idx: slotIdx,
				Key: key,
			}, nil
		}

		return nil, nil
	}

	for randomTry := 1; randomTry <= 10; randomTry++ {
		slotIdx := rand.Intn(slotsSize)
		key := getKVKey(slotIdx)

		maybeSlot, err := trySlot(slotIdx, key)
		if err != nil {
			return nil, err
		}

		if maybeSlot != nil {
			slot = maybeSlot

			break
		}
	}

	if slot == nil {
		// This is a fallback for the case when all slots are taken.
		// There is no Consul lock so it's possible that multiple sandboxes will try to acquire the same slot.
		// In this case, only one of them will succeed and other will try with different slots.
		reservedKeys, _, keysErr := kv.Keys(consul.ClientID+"/", "", nil)
		if keysErr != nil {
			return nil, fmt.Errorf("failed to read Consul KV: %w", keysErr)
		}

		for slotIdx := 0; slotIdx < slotsSize; slotIdx++ {
			key := getKVKey(slotIdx)

			if slices.Contains(reservedKeys, key) {
				continue
			}

			maybeSlot, err := trySlot(slotIdx, key)
			if err != nil {
				return nil, err
			}

			if maybeSlot != nil {
				slot = maybeSlot

				break
			}
		}
	}

	if slot == nil {
		return nil, fmt.Errorf("failed to acquire IP slot: no empty slots found")
	}

	return slot, nil
}

func (ips *Slot) Release() error {
	kv := consul.Client.KV()

	pair, _, err := kv.Get(ips.Key, nil)
	if err != nil {
		return fmt.Errorf("failed to release IPSlot: Failed to read Consul KV: %w", err)
	}

	if pair == nil {
		return fmt.Errorf("IP slot %d was already released", ips.Idx)
	}

	status, _, err := kv.DeleteCAS(&consulApi.KVPair{
		Key:         ips.Key,
		ModifyIndex: pair.ModifyIndex,
	}, nil)
	if err != nil {
		return fmt.Errorf("failed to release IPSlot: Failed to delete slot from Consul KV: %w", err)
	}

	if !status {
		return fmt.Errorf("IP slot '%d' for was already realocated", ips.Idx)
	}

	return nil
}

func getKVKey(slotIdx int) string {
	return fmt.Sprintf("%s/%d", consul.ClientID, slotIdx)
}
