package userfaultfd

import (
	"context"
	"fmt"
	"maps"
	"math/rand"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/uffd/memory"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

func TestNoOperations(t *testing.T) {
	t.Parallel()

	pagesize := uint64(header.PageSize)
	numberOfPages := uint64(512)

	h, err := configureCrossProcessTest(t, testConfig{
		pagesize:      pagesize,
		numberOfPages: numberOfPages,
	})
	require.NoError(t, err)

	// Placeholder uffd that does not serve anything
	uffd, err := NewUserfaultfdFromFd(newMockFd(), h.data, &memory.Mapping{}, zap.L())
	require.NoError(t, err)

	accessedOffsets, err := h.accessedOffsetsOnce()
	require.NoError(t, err)

	assert.Empty(t, accessedOffsets, "checking which pages were faulted")

	dirtyOffsets, err := h.dirtyOffsetsOnce()
	require.NoError(t, err)

	assert.Empty(t, dirtyOffsets, "checking which pages were dirty")

	dirty := uffd.Dirty()
	assert.Empty(t, slices.Collect(dirty.Offsets()), "checking dirty pages")
}

func TestRandomOperations(t *testing.T) {
	t.Parallel()

	pagesize := uint64(header.PageSize)
	numberOfPages := uint64(4096)
	numberOfOperations := 2048
	repetitions := 8

	for i := range repetitions {
		t.Run(fmt.Sprintf("Run_%d_of_%d", i+1, repetitions-1), func(t *testing.T) {
			t.Parallel()

			// Use time-based seed for each run to ensure different random sequences
			// This increases the chance of catching bugs that only manifest with specific sequences
			seed := time.Now().UnixNano() + int64(i)
			rng := rand.New(rand.NewSource(seed))

			t.Logf("Using random seed: %d", seed)

			// Randomly operations on the data
			operations := make([]operation, 0, numberOfOperations)
			for range numberOfOperations {
				operations = append(operations, operation{
					offset: int64(rng.Intn(int(numberOfPages-1)) * int(pagesize)),
					mode:   operationMode(rng.Intn(2) + 1),
				})
			}

			h, err := configureCrossProcessTest(t, testConfig{
				pagesize:      pagesize,
				numberOfPages: numberOfPages,
				operations:    operations,
			})
			require.NoError(t, err)

			for _, operation := range operations {
				err := h.executeOperation(t.Context(), operation)
				require.NoError(t, err, "for operation %+v", operation)
			}

			expectedAccessedOffsets := getOperationsOffsets(operations, operationModeRead|operationModeWrite)

			accessedOffsets, err := h.accessedOffsetsOnce()
			require.NoError(t, err)

			assert.Equal(t, expectedAccessedOffsets, accessedOffsets, "checking which pages were faulted (seed: %d)", seed)

			expectedDirtyOffsets := getOperationsOffsets(operations, operationModeWrite)
			dirtyOffsets, err := h.dirtyOffsetsOnce()
			require.NoError(t, err)

			assert.Equal(t, expectedDirtyOffsets, dirtyOffsets, "checking which pages were dirty (seed: %d)", seed)
		})
	}
}

func TestUffdEvents(t *testing.T) {
	pagesize := uint64(header.PageSize)
	numberOfPages := uint64(32)

	h, err := configureCrossProcessTest(t, testConfig{
		pagesize:      pagesize,
		numberOfPages: numberOfPages,
	})
	require.NoError(t, err)

	mockFd := newMockFd()

	// Placeholder uffd that does not serve anything
	uffd, err := NewUserfaultfdFromFd(mockFd, h.data, &memory.Mapping{}, zap.L())
	require.NoError(t, err)

	events := []event{
		// Same operation and offset, repeated (copies at 0), with both mode 0 and UFFDIO_COPY_MODE_WP
		{
			UffdioCopy: &UffdioCopy{
				dst:  0,
				len:  header.PageSize,
				mode: 0,
			},
			offset: 0,
		},
		{
			UffdioCopy: &UffdioCopy{
				dst:  0,
				len:  header.PageSize,
				mode: UFFDIO_COPY_MODE_WP,
			},
			offset: 0,
		},
		{
			UffdioCopy: &UffdioCopy{
				dst:  0,
				len:  header.PageSize,
				mode: 0,
			},
			offset: 0,
		},
		{
			UffdioCopy: &UffdioCopy{
				dst:  0,
				len:  header.PageSize,
				mode: UFFDIO_COPY_MODE_WP,
			},
			offset: 0,
		},

		// WriteProtect at same offset, repeated
		{
			UffdioWriteProtect: &UffdioWriteProtect{
				_range: UffdioRange{
					start: 0,
					len:   header.PageSize,
				},
			},
			offset: 0,
		},
		{
			UffdioWriteProtect: &UffdioWriteProtect{
				_range: UffdioRange{
					start: 0,
					len:   header.PageSize,
				},
			},
			offset: 0,
		},

		// Copy at next offset, include both mode 0 and UFFDIO_COPY_MODE_WP
		{
			UffdioCopy: &UffdioCopy{
				dst:  header.PageSize,
				len:  header.PageSize,
				mode: 0,
			},
			offset: int64(header.PageSize),
		},
		{
			UffdioCopy: &UffdioCopy{
				dst:  header.PageSize,
				len:  header.PageSize,
				mode: UFFDIO_COPY_MODE_WP,
			},
			offset: int64(header.PageSize),
		},
		{
			UffdioCopy: &UffdioCopy{
				dst:  header.PageSize,
				len:  header.PageSize,
				mode: 0,
			},
			offset: int64(header.PageSize),
		},
		{
			UffdioCopy: &UffdioCopy{
				dst:  header.PageSize,
				len:  header.PageSize,
				mode: UFFDIO_COPY_MODE_WP,
			},
			offset: int64(header.PageSize),
		},

		// WriteProtect at next offset, repeated
		{
			UffdioWriteProtect: &UffdioWriteProtect{
				_range: UffdioRange{
					start: header.PageSize,
					len:   header.PageSize,
				},
			},
			offset: int64(header.PageSize),
		},
		{
			UffdioWriteProtect: &UffdioWriteProtect{
				_range: UffdioRange{
					start: header.PageSize,
					len:   header.PageSize,
				},
			},
			offset: int64(header.PageSize),
		},

		// Copy at another offset, include both mode 0 and UFFDIO_COPY_MODE_WP
		{
			UffdioCopy: &UffdioCopy{
				dst:  2 * header.PageSize,
				len:  header.PageSize,
				mode: 0,
			},
			offset: int64(2 * header.PageSize),
		},
		{
			UffdioCopy: &UffdioCopy{
				dst:  2 * header.PageSize,
				len:  header.PageSize,
				mode: UFFDIO_COPY_MODE_WP,
			},
			offset: int64(2 * header.PageSize),
		},
		{
			UffdioCopy: &UffdioCopy{
				dst:  2 * header.PageSize,
				len:  header.PageSize,
				mode: 0,
			},
			offset: int64(2 * header.PageSize),
		},
		{
			UffdioCopy: &UffdioCopy{
				dst:  2 * header.PageSize,
				len:  header.PageSize,
				mode: UFFDIO_COPY_MODE_WP,
			},
			offset: int64(2 * header.PageSize),
		},

		// WriteProtect at another offset, repeated
		{
			UffdioWriteProtect: &UffdioWriteProtect{
				_range: UffdioRange{
					start: 2 * header.PageSize,
					len:   header.PageSize,
				},
			},
			offset: int64(2 * header.PageSize),
		},
		{
			UffdioWriteProtect: &UffdioWriteProtect{
				_range: UffdioRange{
					start: 2 * header.PageSize,
					len:   header.PageSize,
				},
			},
			offset: int64(2 * header.PageSize),
		},
	}

	for _, event := range events {
		err := event.trigger(t.Context(), uffd)
		require.NoError(t, err, "for event %+v", event)
	}

	receivedEvents := make([]event, 0, len(events))

	for range events {
		select {
		case copyEvent, ok := <-mockFd.copyCh:
			if !ok {
				t.FailNow()
			}

			copyEvent.resolve()

			// We don't add the offset here, because it is propagated only through the "accessed" and "dirty" sets.
			// When later comparing the events, we will compare the events without the offset.
			receivedEvents = append(receivedEvents, event{UffdioCopy: &copyEvent.event})
		case writeProtectEvent, ok := <-mockFd.writeProtectCh:
			if !ok {
				t.FailNow()
			}

			writeProtectEvent.resolve()

			// We don't add the offset here, because it is propagated only through the "accessed" and "dirty" sets.
			// When later comparing the events, we will compare the events without the offset.
			receivedEvents = append(receivedEvents, event{UffdioWriteProtect: &writeProtectEvent.event})
		case <-t.Context().Done():
			return
		}
	}

	assert.Len(t, receivedEvents, len(events), "checking received events")
	assert.ElementsMatch(t, zeroOffsets(events), receivedEvents, "checking received events")

	select {
	case <-mockFd.copyCh:
		t.Fatalf("copy channel should not have any events")
	case <-mockFd.writeProtectCh:
		t.Fatalf("write protect channel should not have any events")
	case <-t.Context().Done():
		t.FailNow()
	default:
	}

	dirty := uffd.Dirty()

	expectedDirtyOffsets := make(map[int64]struct{})
	expectedAccessedOffsets := make(map[int64]struct{})

	for _, event := range events {
		if event.UffdioWriteProtect != nil {
			expectedDirtyOffsets[event.offset] = struct{}{}
		}
		if event.UffdioCopy != nil {
			if event.UffdioCopy.mode != UFFDIO_COPY_MODE_WP {
				expectedDirtyOffsets[event.offset] = struct{}{}
			}

			expectedAccessedOffsets[event.offset] = struct{}{}
		}
	}

	assert.ElementsMatch(t, slices.Collect(maps.Keys(expectedDirtyOffsets)), slices.Collect(dirty.Offsets()), "checking dirty pages")

	accessed := accessed(uffd)
	assert.ElementsMatch(t, slices.Collect(maps.Keys(expectedAccessedOffsets)), slices.Collect(accessed.Offsets()), "checking accessed pages")
}

func TestUffdSettleRequests(t *testing.T) {
	t.Parallel()

	pagesize := uint64(header.PageSize)
	numberOfPages := uint64(32)

	h, err := configureCrossProcessTest(t, testConfig{
		pagesize:      pagesize,
		numberOfPages: numberOfPages,
	})
	require.NoError(t, err)

	testEventsSettle := func(t *testing.T, events []event) {
		t.Helper()

		mockFd := newMockFd()

		// Placeholder uffd that does not serve anything
		uffd, err := NewUserfaultfdFromFd(mockFd, h.data, &memory.Mapping{}, zap.L())
		require.NoError(t, err)

		for _, e := range events {
			err = e.trigger(t.Context(), uffd)
			require.NoError(t, err, "for event %+v", e)
		}

		var blockedCopyEvents []*blockedEvent[UffdioCopy]
		var blockedWriteProtectEvents []*blockedEvent[UffdioWriteProtect]

		for range events {
			// Wait until the event is blocked
			select {
			case copyEvent, ok := <-mockFd.copyCh:
				if !ok {
					t.FailNow()
				}

				require.NotNil(t, copyEvent.event, "copy event should not be nil")
				assert.Contains(t, zeroOffsets(events), event{UffdioCopy: &copyEvent.event}, "checking copy event")

				blockedCopyEvents = append(blockedCopyEvents, copyEvent)
			case writeProtectEvent, ok := <-mockFd.writeProtectCh:
				if !ok {
					t.FailNow()
				}

				require.NotNil(t, writeProtectEvent.event, "write protect event should not be nil")
				assert.Contains(t, zeroOffsets(events), event{UffdioWriteProtect: &writeProtectEvent.event}, "checking write protect event")

				blockedWriteProtectEvents = append(blockedWriteProtectEvents, writeProtectEvent)
			case <-t.Context().Done():
				t.FailNow()
			}
		}

		require.Equal(t, len(blockedCopyEvents)+len(blockedWriteProtectEvents), len(events), "checking blocked events")

		triggerUnlock := make(chan struct{})

		d := make(chan *block.Tracker)

		go func() {
			acquired := uffd.settleRequests.TryLock()
			assert.False(t, acquired, "settleRequests write lock should not be acquired")

			triggerUnlock <- struct{}{}

			// This should block, until the events are resolved.
			dirty := uffd.Dirty()

			select {
			case d <- dirty:
			case <-t.Context().Done():
				return
			}
		}()

		<-triggerUnlock

		// Resolve the events to unblock getting the dirty pages in the goroutine.
		for _, e := range blockedCopyEvents {
			e.resolve()
		}

		for _, e := range blockedWriteProtectEvents {
			e.resolve()
		}

		select {
		case <-mockFd.copyCh:
			t.Fatalf("copy channel should not have any events")
		case <-mockFd.writeProtectCh:
			t.Fatalf("write protect channel should not have any events")
		case <-t.Context().Done():
			t.FailNow()
		case dirty, ok := <-d:
			if !ok {
				t.FailNow()
			}

			assert.ElementsMatch(t, dirtyOffsets(events), slices.Collect(dirty.Offsets()), "checking dirty pages")
		}
	}

	t.Run("missing", func(t *testing.T) {
		t.Parallel()

		events := []event{
			{UffdioCopy: &UffdioCopy{
				dst:  0,
				len:  header.PageSize,
				mode: UFFDIO_COPY_MODE_WP,
			}, offset: 2 * int64(header.PageSize)},
		}

		testEventsSettle(t, events)
	})

	t.Run("write protect", func(t *testing.T) {
		t.Parallel()

		events := []event{
			{UffdioWriteProtect: &UffdioWriteProtect{
				_range: UffdioRange{
					start: 0,
					len:   header.PageSize,
				},
			}, offset: 2 * int64(header.PageSize)},
		}

		testEventsSettle(t, events)
	})

	t.Run("missing write", func(t *testing.T) {
		t.Parallel()

		events := []event{
			{UffdioCopy: &UffdioCopy{
				dst:  0,
				len:  header.PageSize,
				mode: 0,
			}, offset: 2 * int64(header.PageSize)},
		}

		testEventsSettle(t, events)
	})

	t.Run("event mix", func(t *testing.T) {
		t.Parallel()

		events := []event{
			{UffdioCopy: &UffdioCopy{
				dst:  0,
				len:  header.PageSize,
				mode: 0,
			}, offset: 2 * int64(header.PageSize)},
			{UffdioWriteProtect: &UffdioWriteProtect{
				_range: UffdioRange{
					start: 0,
					len:   header.PageSize,
				},
			}, offset: 2 * int64(header.PageSize)},
			{UffdioCopy: &UffdioCopy{
				dst:  0,
				len:  header.PageSize,
				mode: 0,
			}, offset: 2 * int64(header.PageSize)},
			{
				UffdioWriteProtect: &UffdioWriteProtect{
					_range: UffdioRange{
						start: 0,
						len:   header.PageSize,
					},
				}, offset: 0,
			},
		}

		testEventsSettle(t, events)
	})
}

type event struct {
	*UffdioCopy
	*UffdioWriteProtect

	offset int64
}

func (e event) trigger(ctx context.Context, uffd *Userfaultfd) error {
	switch {
	case e.UffdioCopy != nil:
		triggerMissing(ctx, uffd, *e.UffdioCopy, e.offset)
	case e.UffdioWriteProtect != nil:
		triggerWriteProtected(uffd, *e.UffdioWriteProtect, e.offset)
	default:
		return fmt.Errorf("invalid event: %+v", e)
	}

	return nil
}

// Return the event copy without the offset, because the offset is propagated only through the "accessed" and "dirty" sets, so direct comparisons would fail.
func (e event) withoutOffset() event {
	return event{
		UffdioCopy:         e.UffdioCopy,
		UffdioWriteProtect: e.UffdioWriteProtect,
	}
}

// Creates a new slice of events with the offset set to 0, so we can compare the events without the offset.
func zeroOffsets(events []event) []event {
	return utils.Map(events, func(e event) event {
		return e.withoutOffset()
	})
}

func triggerMissing(ctx context.Context, uffd *Userfaultfd, c UffdioCopy, offset int64) {
	var write bool

	if c.mode != UFFDIO_COPY_MODE_WP {
		write = true
	}

	uffd.handleMissing(
		ctx,
		func() error { return nil },
		uintptr(c.dst),
		uintptr(uffd.src.BlockSize()),
		offset,
		write,
	)
}

func triggerWriteProtected(uffd *Userfaultfd, c UffdioWriteProtect, offset int64) {
	uffd.handleWriteProtected(
		func() error { return nil },
		uintptr(c._range.start),
		uintptr(uffd.src.BlockSize()),
		offset,
	)
}

func dirtyOffsets(events []event) []int64 {
	offsets := make(map[int64]struct{})

	for _, e := range events {
		if e.UffdioWriteProtect != nil {
			offsets[e.offset] = struct{}{}
		}

		if e.UffdioCopy != nil {
			if e.UffdioCopy.mode != UFFDIO_COPY_MODE_WP {
				offsets[e.offset] = struct{}{}
			}
		}
	}

	return slices.Collect(maps.Keys(offsets))
}
