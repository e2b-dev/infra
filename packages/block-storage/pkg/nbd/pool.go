package nbd

import (
	"context"
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

// NbdDevicePool requires the nbd module to be loaded before running.
// use `sudo modprobe nbd nbds_max=4096` to set the max number of devices to 4096, which is a good default for now.
type NbdDevicePool struct {
	slots *bitset.BitSet
	mu    sync.Mutex
}

func getMaxNbdDevices() (int, error) {
	data, err := os.ReadFile("/sys/module/nbd/parameters/nbds_max")

	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}

	if err != nil {
		return 0, fmt.Errorf("failed to read nbds_max: %w", err)
	}

	nbdsMax, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("failed to parse nbds_max: %w", err)
	}

	return nbdsMax, nil
}

func NewNbdDevicePool() (*NbdDevicePool, error) {
	maxDevices, err := getMaxNbdDevices()
	if err != nil {
		return nil, fmt.Errorf("failed to get current max devices: %w", err)
	}

	if maxDevices == 0 {
		return nil, fmt.Errorf("nbd module is not loaded or max devices is set to 0")
	}

	return &NbdDevicePool{
		slots: bitset.New(uint(maxDevices)),
	}, nil
}

var re = regexp.MustCompile(`^/dev/nbd(\d+)$`)

func (n *NbdDevicePool) getDeviceSlot(path string) (int, error) {
	matches := re.FindStringSubmatch(path)
	if len(matches) != 2 {
		return 0, fmt.Errorf("invalid nbd path: %s", path)
	}

	slot, err := strconv.ParseInt(matches[1], 10, 0)
	if err != nil {
		return 0, fmt.Errorf("failed to parse slot from path: %w", err)
	}

	return int(slot), nil
}

func (n *NbdDevicePool) getDevicePath(slot uint) string {
	return fmt.Sprintf("/dev/nbd%d", slot)
}

func (n *NbdDevicePool) GetDevice() (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	slot, ok := n.slots.NextClear(0)
	if !ok {
		return "", fmt.Errorf("no free slots")
	}

	n.slots.Set(slot)

	pidFile := fmt.Sprintf("/sys/block/nbd%d/pid", slot)

	_, err := os.Stat(pidFile)
	if errors.Is(err, os.ErrNotExist) {
		return n.getDevicePath(slot), nil
	}

	return "", fmt.Errorf("device in use: %s", pidFile)
}

func (n *NbdDevicePool) ReleaseDevice(ctx context.Context, path string) error {
	var errs []error

	out, err := exec.CommandContext(ctx, "umount", "--all-targets", path).CombinedOutput()
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to umount device: %w: %s", err, string(out)))
	}

	slot, err := n.getDeviceSlot(path)
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to get slot from path: %w", err))

		return errors.Join(errs...)
	}

	n.mu.Lock()
	n.slots.Clear(uint(slot))
	n.mu.Unlock()

	return errors.Join(errs...)
}
