package nbd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/bits-and-blooms/bitset"
)

var (
	// ErrNoFreeSlots is returned when there are no free slots.
	// You can retry the request after some time.
	ErrNoFreeSlots = errors.New("no free slots")
	// ErrDeviceInUse is returned when the device that you wanted to release is still in use.
	// You can retry the request after ensuring that the device is not in use anymore.
	ErrDeviceInUse = errors.New("device in use")
)

type (
	// DevicePath is the path to the nbd device.
	DevicePath = string
	// DeviceSlot is the slot number of the nbd device.
	DeviceSlot = uint
)

// DevicePool requires the nbd module to be loaded before running.
//
// Use `sudo modprobe nbd nbds_max=4096` to set the max number of devices to 4096, which is a good default for now.
type DevicePool struct {
	// We use the bitset to speedup the free device lookup.
	slots *bitset.BitSet
	mu    sync.Mutex
}

func getMaxDevices() (uint, error) {
	data, err := os.ReadFile("/sys/module/nbd/parameters/nbds_max")

	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}

	if err != nil {
		return 0, fmt.Errorf("failed to read nbds_max: %w", err)
	}

	maxDevices, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 0)
	if err != nil {
		return 0, fmt.Errorf("failed to parse nbds_max: %w", err)
	}

	return uint(maxDevices), nil
}

func NewDevicePool() (*DevicePool, error) {
	maxDevices, err := getMaxDevices()
	if err != nil {
		return nil, fmt.Errorf("failed to get current max devices: %w", err)
	}

	if maxDevices == 0 {
		return nil, fmt.Errorf("nbd module is not loaded or max devices is set to 0")
	}

	return &DevicePool{
		slots: bitset.New(maxDevices),
	}, nil
}

var reSlot = regexp.MustCompile(`^/dev/nbd(\d+)$`)

func (n *DevicePool) getDeviceSlot(path DevicePath) (DeviceSlot, error) {
	matches := reSlot.FindStringSubmatch(path)
	if len(matches) != 2 {
		return 0, fmt.Errorf("invalid nbd path: %s", path)
	}

	slot, err := strconv.ParseUint(matches[1], 10, 0)
	if err != nil {
		return 0, fmt.Errorf("failed to parse slot from path: %w", err)
	}

	return DeviceSlot(slot), nil
}

func (n *DevicePool) getDevicePath(slot DeviceSlot) DevicePath {
	return fmt.Sprintf("/dev/nbd%d", slot)
}

func (n *DevicePool) isDeviceFree(slot DeviceSlot) (bool, error) {
	pidFile := fmt.Sprintf("/sys/block/nbd%d/pid", slot)

	_, err := os.Stat(pidFile)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}

	if err != nil {
		return false, fmt.Errorf("failed to check if device is busy: %w", err)
	}

	return false, nil
}

// Get device slot if there is one available.
func (n *DevicePool) GetDevice() (DevicePath, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	for slot, ok := n.slots.NextClear(0); ok; slot, ok = n.slots.NextClear(slot + 1) {
		n.slots.Set(slot)

		free, err := n.isDeviceFree(slot)
		if err != nil {
			n.slots.Clear(slot)

			return "", fmt.Errorf("failed to check if device is free: %w", err)
		}

		if free {
			return n.getDevicePath(slot), nil
		}

		// We clear the slot even though it is not free to prevent accidental accumulation of slots.
		n.slots.Clear(slot)
	}

	return "", ErrNoFreeSlots
}

// ReleaseDevice will unmount the device from all targets and release the slot.
// It will return an error if the device is not free and not release the slot — you can retry.
func (n *DevicePool) ReleaseDevice(path DevicePath) error {
	// Preventively unmount the device from all targets.
	out, err := exec.Command("umount", "--all-targets", path).CombinedOutput()
	if err != nil {
		// Suppres unmount errors if the device is not mounted.
		if !strings.HasSuffix(string(out), "not mounted\n") {
			return fmt.Errorf("failed to umount device: %w: %s", err, string(out))
		}
	}

	slot, err := n.getDeviceSlot(path)
	if err != nil {
		return fmt.Errorf("failed to get slot from path: %w", err)
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	free, err := n.isDeviceFree(slot)
	if err != nil {
		return fmt.Errorf("failed to check if device is free: %w", err)
	}

	if !free {
		return ErrDeviceInUse
	}

	n.slots.Clear(slot)

	return nil
}
