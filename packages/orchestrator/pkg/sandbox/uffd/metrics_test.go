//go:build linux

package uffd

import (
	"math/rand"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/assert"
)

// TestDivergenceCardinalities checks the AndCardinality-based subtraction
// against the definitional AndNot formulation on fixed shapes and randomized
// bitmaps: trackerOnly = |tracker \ pagemap|, pagemapOnly = |pagemap \
// tracker|, pagemapDirty = |pagemap|.
func TestDivergenceCardinalities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tracker []uint32
		pagemap []uint32
	}{
		{name: "both empty", tracker: nil, pagemap: nil},
		{name: "identical", tracker: []uint32{1, 2, 3}, pagemap: []uint32{1, 2, 3}},
		{name: "disjoint", tracker: []uint32{1, 2}, pagemap: []uint32{3, 4, 5}},
		{name: "tracker superset", tracker: []uint32{1, 2, 3, 4}, pagemap: []uint32{2, 3}},
		{name: "pagemap superset", tracker: []uint32{2, 3}, pagemap: []uint32{1, 2, 3, 4}},
		{name: "tracker empty", tracker: nil, pagemap: []uint32{7, 9}},
		{name: "pagemap empty", tracker: []uint32{7, 9}, pagemap: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertMatchesAndNot(t, roaring.BitmapOf(tt.tracker...), roaring.BitmapOf(tt.pagemap...))
		})
	}

	t.Run("randomized", func(t *testing.T) {
		t.Parallel()
		rng := rand.New(rand.NewSource(1))
		for range 100 {
			tracker, pagemap := roaring.New(), roaring.New()
			for range rng.Intn(2000) {
				tracker.Add(uint32(rng.Intn(4096)))
			}
			for range rng.Intn(2000) {
				pagemap.Add(uint32(rng.Intn(4096)))
			}
			assertMatchesAndNot(t, tracker, pagemap)
		}
	})
}

func assertMatchesAndNot(t *testing.T, tracker, pagemap *roaring.Bitmap) {
	t.Helper()

	trackerOnly, pagemapOnly, pagemapDirty := divergenceCardinalities(tracker, pagemap)

	assert.Equal(t, roaring.AndNot(tracker, pagemap).GetCardinality(), trackerOnly, "trackerOnly")
	assert.Equal(t, roaring.AndNot(pagemap, tracker).GetCardinality(), pagemapOnly, "pagemapOnly")
	assert.Equal(t, pagemap.GetCardinality(), pagemapDirty, "pagemapDirty")
}
