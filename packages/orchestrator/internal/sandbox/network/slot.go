package network

import (
	"fmt"
	"math/rand"
	"slices"

	consul "github.com/hashicorp/consul/api"

	client "github.com/e2b-dev/infra/packages/orchestrator/internal/consul"
)

// We are using a more debuggable IP address allocation for now that only covers 255 addresses.
const (
	octetSize = 256
	octetMax  = octetSize - 1
	// This is the maximum number of IP addresses that can be allocated.
	ipSlotsSize = octetSize * octetSize

	hostMask = 32
	vMask    = 30
	tapMask  = 30
)

type IPSlot struct {
	ConsulToken string

	KVKey   string
	SlotIdx int
}

func (ips *IPSlot) VpeerName() string {
	return "eth0"
}

func (ips *IPSlot) getOctets() (int, int) {
	rem := ips.SlotIdx % octetSize
	octet := (ips.SlotIdx - rem) / octetSize

	return octet, rem
}

func (ips *IPSlot) VpeerIP() string {
	firstOctet, secondOctet := ips.getOctets()

	return fmt.Sprintf("10.%d.%d.2", firstOctet, secondOctet)
}

func (ips *IPSlot) VethIP() string {
	firstOctet, secondOctet := ips.getOctets()

	return fmt.Sprintf("10.%d.%d.1", firstOctet, secondOctet)
}

func (ips *IPSlot) VMask() int {
	return vMask
}

func (ips *IPSlot) VethName() string {
	return fmt.Sprintf("veth-%d", ips.SlotIdx)
}

func (ips *IPSlot) VethCIDR() string {
	return fmt.Sprintf("%s/%d", ips.VethIP(), ips.VMask())
}

func (ips *IPSlot) VpeerCIDR() string {
	return fmt.Sprintf("%s/%d", ips.VpeerIP(), ips.VMask())
}

func (ips *IPSlot) HostCIDR() string {
	return fmt.Sprintf("%s/%d", ips.HostIP(), ips.HostMask())
}

func (ips *IPSlot) HostMask() int {
	return hostMask
}

// IP address for the sandbox from the host machine.
// You can use it to make requests to the sandbox.
func (ips *IPSlot) HostIP() string {
	firstOctet, secondOctet := ips.getOctets()

	return fmt.Sprintf("192.168.%d.%d", firstOctet, secondOctet)
}

func (ips *IPSlot) NamespaceIP() string {
	return "169.254.0.21"
}

func (ips *IPSlot) NamespaceID() string {
	return fmt.Sprintf("ns-%d", ips.SlotIdx)
}

func (ips *IPSlot) TapName() string {
	return "tap0"
}

func (ips *IPSlot) TapIP() string {
	return "169.254.0.22"
}

func (ips *IPSlot) TapMask() int {
	return tapMask
}

func (ips *IPSlot) TapCIDR() string {
	return fmt.Sprintf("%s/%d", ips.TapIP(), ips.TapMask())
}

func NewSlot(consulClient *consul.Client) (*IPSlot, error) {
	kv := consulClient.KV()

	var slot *IPSlot

	trySlot := func(slotIdx int, key string) (*IPSlot, error) {
		status, _, err := kv.CAS(&consul.KVPair{
			Key:         key,
			ModifyIndex: 0,
		}, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to write to Consul KV: %w", err)
		}

		if status {
			return &IPSlot{
				SlotIdx: slotIdx,
				KVKey:   key,
			}, nil
		}

		return nil, nil
	}

	for randomTry := 1; randomTry <= 10; randomTry++ {
		slotIdx := rand.Intn(ipSlotsSize)
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
		reservedKeys, _, keysErr := kv.Keys(client.ClientID+"/", "", nil)
		if keysErr != nil {
			return nil, fmt.Errorf("failed to read Consul KV: %w", keysErr)
		}

		for slotIdx := 0; slotIdx < ipSlotsSize; slotIdx++ {
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

func (ips *IPSlot) Release(consulClient *consul.Client) error {
	kv := consulClient.KV()

	pair, _, err := kv.Get(ips.KVKey, nil)
	if err != nil {
		return fmt.Errorf("failed to release IPSlot: Failed to read Consul KV: %w", err)
	}

	if pair == nil {
		return fmt.Errorf("IP slot %d was already released", ips.SlotIdx)
	}

	status, _, err := kv.DeleteCAS(&consul.KVPair{
		Key:         ips.KVKey,
		ModifyIndex: pair.ModifyIndex,
	}, nil)
	if err != nil {
		return fmt.Errorf("failed to release IPSlot: Failed to delete slot from Consul KV: %w", err)
	}

	if !status {
		return fmt.Errorf("IP slot '%d' for was already realocated", ips.SlotIdx)
	}

	return nil
}

func getKVKey(slotIdx int) string {
	return fmt.Sprintf("%s/%d", client.ClientID, slotIdx)
}
