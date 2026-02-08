package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
)

var experiments = map[string]map[string]experiment{
	"concurrent requests": {
		// "1": &setConcurrentRequests{1},
		// "2": &setConcurrentRequests{2},
		// "4": &setConcurrentRequests{4},
		"8":  &setConcurrentRequests{8},
		"16": &setConcurrentRequests{16},
		"32": &setConcurrentRequests{32},
	},
	"read method": {
		"ReadAt": readMethodExperiment{func(path string) (int, error) {
			fp, err := os.Open(path)
			if err != nil {
				return 0, fmt.Errorf("failed to open file: %w", err)
			}
			defer safeClose(fp)

			buff := make([]byte, expectedFileSize)

			return fp.ReadAt(buff, 0)
		}},
		// "Read": readMethodExperiment{func(path string) (int, error) {
		//	fp, err := os.Open(path)
		//	if err != nil {
		//		return 0, fmt.Errorf("failed to open file: %w", err)
		//	}
		//	defer safeClose(fp)
		//
		//	buff := make([]byte, defaultChunkSize)
		//
		//	var total int
		//	for {
		//		n, err := fp.Read(buff)
		//		total += n
		//
		//		if err != nil {
		//			if err == io.EOF {
		//				break
		//			}
		//
		//			return total, fmt.Errorf("failed to read from file: %w", err)
		//		}
		//	}
		//
		//	return total, nil
		// }},
	},
	"nfs read ahead": {
		// "128kb (default)": &setReadAhead{readAhead: "128"}, // always bad
		"4mb": &setReadAhead{readAhead: "4096"},
	},
	"net.core.rmem_max": {
		"208kb (default)": &setSysFs{path: "net.core.rmem_max", newValue: "212992"},
		// "32mb":            &setSysFs{path: "net.core.rmem_max", newValue: "33554432"},
	},
	"net.ipv4.tcp_rmem": {
		"4 kb / 128 kb / 6 mb (default)": &setSysFs{path: "net.ipv4.tcp_rmem", newValue: "4096 131072 6291456"},
		// "4 kb / 256 kb / 32 mb":          &setSysFs{path: "net.ipv4.tcp_rmem", newValue: "4096 262144 33554432"},
	},
	"sunrpc.tcp_slot_table_entries": {
		"2 (default)": &setSysFs{path: "sunrpc.tcp_slot_table_entries", newValue: "2"},
		"128":         &setSysFs{path: "sunrpc.tcp_slot_table_entries", newValue: "128"},
	},
	"read count": {
		"100": &setReadCount{100},
	},
	"skip count": {
		"0": &setSkipCount{0},
	},
	"allow repeat reads": {
		"disabled": nil,
		"enabled":  &setAllowRepeatReads{true},
	},
}

type experiment interface {
	setup(ctx context.Context, p *processor) error
	teardown(ctx context.Context, p *processor) error
}

type element struct {
	name string
	exp  experiment
}

type scenario struct {
	elements map[string]element
}

func (s scenario) setup(ctx context.Context, p *processor) error {
	var errs []error

	for _, e := range s.elements {
		if e.exp != nil {
			if err := e.exp.setup(ctx, p); err != nil {
				errs = append(errs, fmt.Errorf("failed to setup %q: %w", e, err))
			}
		}
	}

	return errors.Join(errs...)
}

func (s scenario) teardown(ctx context.Context, p *processor) error {
	var errs []error

	for name, e := range s.elements {
		if e.exp != nil {
			if err := e.exp.teardown(ctx, p); err != nil {
				errs = append(errs, fmt.Errorf("failed to teardown %q: %w", name, err))
			}
		}
	}

	return errors.Join(errs...)
}

func (s scenario) Name() any {
	var keys []string
	for k := range s.elements {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	var values []string
	for _, k := range keys {
		values = append(values, fmt.Sprintf("%s=%s", k, s.elements[k].name))
	}

	return strings.Join(values, "; ")
}

type setReadAhead struct {
	readAhead string

	readAheadPath string
	oldReadAhead  string
}

func (s *setReadAhead) setup(ctx context.Context, p *processor) error {
	// find nfs device
	output, err := exec.CommandContext(ctx, "findmnt", "--noheadings", "--output", "target", "--target", p.path).Output()
	if err != nil {
		return fmt.Errorf("failed to find nfs mount point: %w", err)
	}
	nfsMountPoint := strings.TrimSpace(string(output))

	// find major:minor of device
	majorMinor, err := exec.CommandContext(ctx, "mountpoint", "--fs-devno", nfsMountPoint).Output()
	if err != nil {
		return fmt.Errorf("failed to find nfs major:minor: %w", err)
	}
	s.readAheadPath = fmt.Sprintf("/sys/class/bdi/%s/read_ahead_kb", strings.TrimSpace(string(majorMinor)))

	// read old read_ahead_kb, store
	output, err = os.ReadFile(s.readAheadPath)
	if err != nil {
		return fmt.Errorf("failed to read read_ahead_kb: %w", err)
	}

	s.oldReadAhead = strings.TrimSpace(string(output))

	// set new value
	if err := os.WriteFile(s.readAheadPath, []byte(s.readAhead), 0o644); err != nil {
		return fmt.Errorf("failed to write to %q: %w", s.readAheadPath, err)
	}

	return nil
}

func (s *setReadAhead) teardown(_ context.Context, _ *processor) error {
	// reset old value
	return os.WriteFile(s.readAheadPath, []byte(s.oldReadAhead), 0o644)
}

var _ experiment = (*setReadAhead)(nil)

type setConcurrentRequests struct {
	concurrentRequests int
}

func (s *setConcurrentRequests) setup(_ context.Context, p *processor) error {
	p.concurrentRequests = s.concurrentRequests

	return nil
}

func (s *setConcurrentRequests) teardown(_ context.Context, _ *processor) error { return nil }

type setSysFs struct {
	path     string
	newValue string
	oldValue string
}

var _ experiment = (*setSysFs)(nil)

func (d *setSysFs) setup(ctx context.Context, _ *processor) error {
	// read old value
	output, err := exec.CommandContext(ctx, "sysctl", "-n", d.path).Output()
	if err != nil {
		return fmt.Errorf("failed to read sysfs value: %w", err)
	}
	d.oldValue = strings.TrimSpace(string(output))

	// set new value
	if err := exec.CommandContext(ctx, "sysctl", "-w", fmt.Sprintf("%s=%s", d.path, d.newValue)).Run(); err != nil {
		return fmt.Errorf("failed to set sysfs value: %w", err)
	}

	return nil
}

func (d *setSysFs) teardown(ctx context.Context, _ *processor) error {
	// set old value
	if err := exec.CommandContext(ctx, "sysctl", "-w", fmt.Sprintf("%s=%s", d.path, d.oldValue)).Run(); err != nil {
		return fmt.Errorf("failed to set sysfs value: %w", err)
	}

	return nil
}

type readMethodExperiment struct {
	readMethod func(string) (int, error)
}

func (r readMethodExperiment) setup(_ context.Context, p *processor) error {
	p.readMethod = r.readMethod

	return nil
}

func (r readMethodExperiment) teardown(_ context.Context, _ *processor) error {
	return nil
}

var _ experiment = (*readMethodExperiment)(nil)

type setReadCount struct {
	readCount int
}

func (s *setReadCount) setup(_ context.Context, p *processor) error {
	p.readCount = s.readCount

	return nil
}

func (s *setReadCount) teardown(_ context.Context, _ *processor) error { return nil }

var _ experiment = (*setReadCount)(nil)

type setSkipCount struct {
	skipCount int
}

func (s *setSkipCount) setup(_ context.Context, p *processor) error {
	p.skipCount = s.skipCount

	return nil
}

func (s *setSkipCount) teardown(_ context.Context, _ *processor) error { return nil }

var _ experiment = (*setSkipCount)(nil)

type setAllowRepeatReads struct {
	allowRepeatReads bool
}

func (s *setAllowRepeatReads) setup(_ context.Context, p *processor) error {
	p.allowRepeatReads = s.allowRepeatReads

	return nil
}

func (s *setAllowRepeatReads) teardown(_ context.Context, _ *processor) error { return nil }

var _ experiment = (*setAllowRepeatReads)(nil)
