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

	"github.com/bits-and-blooms/bitset"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/meters"
)

// maxSlotsReady is the number of slots that are ready to be used.
const maxSlotsReady = 64

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
	DeviceSlot = uint32
)

// DevicePool requires the nbd module to be loaded before running.
//
// Use `sudo modprobe nbd nbds_max=4096` to set the max number of devices to 4096, which is a good default for now.
type DevicePool struct {
	ctx context.Context
	// We use the bitset to speedup the free device lookup.
	usedSlots *bitset.BitSet
	mu        sync.Mutex

	slots chan DeviceSlot

	slotCounter metric.Int64UpDownCounter
}

func MustGetDevicePool() *DevicePool {
	maxDevices, err := getMaxDevices()
	if err != nil {
		panic(fmt.Errorf("failed to get current max devices: %w", err))
	}

	if maxDevices == 0 {
		panic(fmt.Errorf("nbd module is not loaded or max devices is set to 0"))
	}

	counter, err := meters.GetUpDownCounter(meters.NBDkSlotSReadyPoolCounterMeterName)
	if err != nil {
		panic(fmt.Errorf("failed to get nbd slot pool counter: %w", err))
	}

	pool := &DevicePool{
		ctx:         context.Background(),
		usedSlots:   bitset.New(maxDevices),
		slots:       make(chan DeviceSlot, maxSlotsReady),
		slotCounter: counter,
	}

	go func() {
		err = pool.Populate()
		if err != nil {
			zap.L().Fatal("failed during populating device pool", zap.Error(err))
		}
	}()

	return pool
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

func (d *DevicePool) Populate() error {
	defer close(d.slots)

	for {
		select {
		case <-d.ctx.Done():
			return d.ctx.Err()
		default:
			device, err := d.getFreeDeviceSlot()
			if err != nil {
				zap.L().Error("[nbd pool]: failed to create network", zap.Error(err))

				continue
			}

			d.slotCounter.Add(d.ctx, 1)
			d.slots <- *device
		}
	}
}

// The following files and resources are useful for checking if the device is free:
// /sys/devices/virtual/block/nbdX/pid
// /sys/block/nbdX/pid
// /sys/block/nbdX/size
// nbd-client -c
// https://unix.stackexchange.com/questions/33508/check-which-network-block-devices-are-in-use
// https://superuser.com/questions/919895/how-to-get-a-list-of-connected-nbd-devices-on-ubuntu
// https://github.com/NetworkBlockDevice/nbd/blob/17043b068f4323078637314258158aebbfff0a6c/nbd-client.c#L254
func (d *DevicePool) isDeviceFree(slot DeviceSlot) (bool, error) {
	// Continue only if the file doesn't exist.
	pidFile := fmt.Sprintf("/sys/block/nbd%d/pid", slot)

	_, err := os.Stat(pidFile)
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

func (d *DevicePool) getMaybeEmptySlot(start DeviceSlot) (DeviceSlot, func(), bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	slot, ok := d.usedSlots.NextClear(uint(start))

	if !ok {
		return 0, func() {}, false
	}

	d.usedSlots.Set(slot)

	return uint32(slot), func() {
		d.mu.Lock()
		defer d.mu.Unlock()

		d.usedSlots.Clear(slot)
	}, true
}

// Get a free device slot.
func (d *DevicePool) getFreeDeviceSlot() (*DeviceSlot, error) {
	start := uint32(0)

	for {
		slot, cleanup, ok := d.getMaybeEmptySlot(start)

		if !ok {
			cleanup()

			return nil, ErrNoFreeSlots{}
		}

		free, err := d.isDeviceFree(slot)
		if err != nil {
			cleanup()

			return nil, fmt.Errorf("failed to check if device is free: %w", err)
		}

		if !free {
			// We clear the slot even though it is not free to prevent accidental accumulation of slots.
			cleanup()

			// We increment the start to avoid infinite loops.
			start++

			continue
		}

		return &slot, nil
	}
}

// Get device slot if there is one available.
func (d *DevicePool) GetDevice(ctx context.Context) (DeviceSlot, error) {
	for {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}

		slot := <-d.slots
		d.slotCounter.Add(d.ctx, -1)

		return slot, nil
	}
}

// ReleaseDevice will return an error if the device is not free and not release the slot — you can retry.
func (d *DevicePool) ReleaseDevice(idx DeviceSlot) error {
	free, err := d.isDeviceFree(idx)
	if err != nil {
		return fmt.Errorf("failed to check if device is free: %w", err)
	}

	if !free {
		return ErrDeviceInUse{}
	}

	d.mu.Lock()
	d.usedSlots.Clear(uint(idx))
	d.mu.Unlock()

	return nil
}

func GetDevicePath(slot DeviceSlot) DevicePath {
	return fmt.Sprintf("/dev/nbd%d", slot)
}

var reSlot = regexp.MustCompile(`^/dev/nbd(\d+)$`)

func GetDeviceSlot(path DevicePath) (DeviceSlot, error) {
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
