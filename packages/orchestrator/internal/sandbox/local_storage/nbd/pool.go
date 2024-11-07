package nbd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/bits-and-blooms/bitset"
)

// ErrNoFreeSlots is returned when there are no free slots.
// You can retry the request after some time.
type ErrNoFreeSlots struct{}

func (ErrNoFreeSlots) Error() string {
	return "no free slots"
}

// ErrDeviceInUse is returned when the device that you wanted to release is still in use.
// You can retry the request after ensuring that the device is not in use anymore.
type ErrDeviceInUse struct{}

func (ErrDeviceInUse) Error() string {
	return "device in use"
}

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

var Pool *DevicePool

func init() {
	pool, err := NewDevicePool()
	if err != nil {
		panic(err)
	}

	Pool = pool
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

// The following files and resources are useful for checking if the device is free:
// /sys/devices/virtual/block/nbdX/pid
// /sys/block/nbdX/pid
// /sys/block/nbdX/size
// nbd-client -c
// https://unix.stackexchange.com/questions/33508/check-which-network-block-devices-are-in-use
// https://superuser.com/questions/919895/how-to-get-a-list-of-connected-nbd-devices-on-ubuntu
// https://github.com/NetworkBlockDevice/nbd/blob/17043b068f4323078637314258158aebbfff0a6c/nbd-client.c#L254
func (n *DevicePool) isDeviceFree(slot DeviceSlot) (bool, error) {
	devicePath := n.getDevicePath(slot)

	fd, err := syscall.Open(devicePath, syscall.O_EXCL, 0o644)
	if errors.Is(err, syscall.EBUSY) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("failed to open device: %w", err)
	}

	err = syscall.Close(fd)
	if err != nil {
		return false, fmt.Errorf("failed to close device: %w", err)
	}

	// Continue only if the file doesn't exist.
	pidFile := fmt.Sprintf("/sys/block/nbd%d/pid", slot)

	_, err = os.Stat(pidFile)
	if err == nil {
		// File is present, therefore the device is in use.
		return false, nil
	}

	if !os.IsNotExist(err) {
		// Some other error occurred.
		return false, fmt.Errorf("failed to stat pid file: %w", err)
	}

	sizeFile := fmt.Sprintf("/sys/block/nbd%d/size", slot)

	data, err := os.ReadFile(sizeFile)
	if err != nil {
		return false, fmt.Errorf("failed to read size file: %w", err)
	}

	sizeStr := strings.TrimSpace(string(data))

	size, err := strconv.ParseUint(sizeStr, 10, 64)
	if err != nil {
		return false, fmt.Errorf("failed to parse size: %w", err)
	}

	return size == 0, nil
}

func (n *DevicePool) getMaybeEmptySlot(start DeviceSlot) (DeviceSlot, func(), bool) {
	n.mu.Lock()
	defer n.mu.Unlock()

	slot, ok := n.slots.NextClear(start)

	if !ok {
		return 0, func() {}, false
	}

	n.slots.Set(slot)

	return slot, func() {
		n.mu.Lock()
		defer n.mu.Unlock()

		n.slots.Clear(slot)
	}, true
}

// Get device slot if there is one available.
func (n *DevicePool) GetDevice(ctx context.Context) (DevicePath, error) {
	start := uint(0)

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		slot, cleanup, ok := n.getMaybeEmptySlot(start)

		if !ok {
			cleanup()

			return "", ErrNoFreeSlots{}
		}

		free, err := n.isDeviceFree(slot)
		if err != nil {
			cleanup()

			return "", fmt.Errorf("failed to check if device is free: %w", err)
		}

		if !free {
			// We clear the slot even though it is not free to prevent accidental accumulation of slots.
			cleanup()

			// We increment the start to avoid infinite loops.
			start++

			continue
		}

		// log.Printf("got device slot: %d\n", slot)

		return n.getDevicePath(slot), nil
	}
}

// ReleaseDevice will return an error if the device is not free and not release the slot — you can retry.
func (n *DevicePool) ReleaseDevice(path DevicePath) error {
	slot, err := n.getDeviceSlot(path)
	if err != nil {
		return fmt.Errorf("failed to get slot from path: %w", err)
	}

	free, err := n.isDeviceFree(slot)
	if err != nil {
		return fmt.Errorf("failed to check if device is free: %w", err)
	}

	if !free {
		return ErrDeviceInUse{}
	}

	// log.Printf("released device slot: %d\n", slot)
	n.mu.Lock()
	n.slots.Clear(slot)
	n.mu.Unlock()

	return nil
}
