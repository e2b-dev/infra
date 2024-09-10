package nbd

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/bits-and-blooms/bitset"
	"github.com/pmorjan/kmod"
)

const (
	maxNbdDevices = 4096
)

type NbdModule struct {
	kmod            *kmod.Kmod
	slots           *bitset.BitSet
	mu              sync.Mutex
	NumberOfDevices int
}

func NewNbdModule(numberOfDevices int) (*NbdModule, error) {
	k, err := kmod.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create kmod: %w", err)
	}

	return &NbdModule{
		NumberOfDevices: numberOfDevices,
		kmod:            k,
		slots:           bitset.New(uint(numberOfDevices)),
	}, nil
}

func (n *NbdModule) getCurrentMaxDevices() (int, error) {
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

func (n *NbdModule) Init() error {
	currentMaxDevices, err := n.getCurrentMaxDevices()
	if err != nil {
		return fmt.Errorf("failed to get current max devices: %w", err)
	}

	if currentMaxDevices == n.NumberOfDevices {
		return nil
	}

	if currentMaxDevices != 0 {
		err = n.kmod.Unload("nbd")
		if err != nil {
			return fmt.Errorf("failed to unload nbd module: %w", err)
		}
	}

	err = n.kmod.Load("nbd", fmt.Sprintf("nbds_max=%d", n.NumberOfDevices), 0)
	if err != nil {
		return fmt.Errorf("failed to load nbd module: %w", err)
	}

	return nil
}

var re = regexp.MustCompile(`^/dev/nbd(\d+)$`)

func (n *NbdModule) getSlotFromPath(path string) (int, error) {
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

func (n *NbdModule) getDevicePath(slot uint) string {
	return fmt.Sprintf("/dev/nbd%d", slot)
}

func (n *NbdModule) GetDevice() (string, error) {
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

	return "", fmt.Errorf("device is in use: %s", pidFile)
}

func (n *NbdModule) ReleaseDevice(path string) error {
	slot, err := n.getSlotFromPath(path)
	if err != nil {
		return fmt.Errorf("failed to get slot from path: %w", err)
	}

	n.slots.Clear(uint(slot))

	return nil
}
