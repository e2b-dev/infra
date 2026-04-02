package atomicbitset

import (
	"iter"
	"math/bits"
	"sync/atomic"
)

// DefaultShardBits is 128 MB / 4 KB = 32768 bits per shard (4 KB bitmap, one OS page).
const DefaultShardBits uint = 32768

type shard struct {
	words []atomic.Uint64
}

func newShard(bitsPerShard uint) *shard {
	return &shard{
		words: make([]atomic.Uint64, (bitsPerShard+63)/64),
	}
}

// Sharded is a two-level lock-free bitset with lazily allocated shard bitmaps.
type Sharded struct {
	shards       []atomic.Pointer[shard]
	n            uint
	bitsPerShard uint
}

func NewSharded(n, bitsPerShard uint) *Sharded {
	if bitsPerShard == 0 {
		bitsPerShard = DefaultShardBits
	}
	numShards := (n + bitsPerShard - 1) / bitsPerShard

	return &Sharded{
		shards:       make([]atomic.Pointer[shard], numShards),
		n:            n,
		bitsPerShard: bitsPerShard,
	}
}

func (s *Sharded) Len() uint { return s.n }

func (s *Sharded) getShard(idx uint) *shard {
	return s.shards[idx].Load()
}

func (s *Sharded) getOrCreateShard(idx uint) *shard {
	p := &s.shards[idx]
	sh := p.Load()
	if sh != nil {
		return sh
	}
	candidate := newShard(s.bitsPerShard)
	if p.CompareAndSwap(nil, candidate) {
		return candidate
	}

	return p.Load()
}

func (s *Sharded) Has(i uint) bool {
	if i >= s.n {
		return false
	}
	sh := s.getShard(i / s.bitsPerShard)
	if sh == nil {
		return false
	}
	local := i % s.bitsPerShard
	wordIndex := local / 64
	bitIndex := local % 64

	mask := uint64(1) << bitIndex

	return sh.words[wordIndex].Load()&mask != 0
}

func (s *Sharded) HasRange(lo, hi uint) bool {
	if lo >= hi {
		return true
	}
	if hi > s.n {
		hi = s.n
	}
	if lo >= hi {
		return false
	}

	for i := lo; i < hi; {
		shardIdx := i / s.bitsPerShard
		localLo := i % s.bitsPerShard
		localHi := min(hi-shardIdx*s.bitsPerShard, s.bitsPerShard)

		sh := s.getShard(shardIdx)
		if sh == nil {
			return false
		}
		if !shardHasRange(sh, localLo, localHi) {
			return false
		}

		i = (shardIdx + 1) * s.bitsPerShard
	}

	return true
}

func (s *Sharded) SetRange(lo, hi uint) {
	if hi > s.n {
		hi = s.n
	}
	if lo >= hi {
		return
	}

	for i := lo; i < hi; {
		shardIdx := i / s.bitsPerShard
		localLo := i % s.bitsPerShard
		localHi := min(hi-shardIdx*s.bitsPerShard, s.bitsPerShard)

		shardSetRange(s.getOrCreateShard(shardIdx), localLo, localHi)

		i = (shardIdx + 1) * s.bitsPerShard
	}
}

func (s *Sharded) UnsafeIterator() iter.Seq[uint] { return s.Iterator() }

func (s *Sharded) Iterator() iter.Seq[uint] {
	return func(yield func(uint) bool) {
		for si := range s.shards {
			sh := s.shards[si].Load()
			if sh == nil {
				continue
			}
			base := uint(si) * s.bitsPerShard
			for wi := range sh.words {
				word := sh.words[wi].Load()
				wordBase := base + uint(wi)*64
				for word != 0 {
					tz := uint(bits.TrailingZeros64(word))
					idx := wordBase + tz
					if idx >= s.n {
						return
					}
					if !yield(idx) {
						return
					}
					word &= word - 1
				}
			}
		}
	}
}

func shardHasRange(sh *shard, lo, hi uint) bool {
	for i := lo; i < hi; {
		w := i / 64
		bit := i % 64
		top := min(hi-w*64, 64)
		mask := wordMask(bit, top)

		if sh.words[w].Load()&mask != mask {
			return false
		}
		i = (w + 1) * 64
	}

	return true
}

func shardSetRange(sh *shard, lo, hi uint) {
	for i := lo; i < hi; {
		w := i / 64
		bit := i % 64
		top := min(hi-w*64, 64)

		sh.words[w].Or(wordMask(bit, top))

		i = (w + 1) * 64
	}
}

var (
	_ Bitset = (*Flat)(nil)
	_ Bitset = (*Roaring)(nil)
	_ Bitset = (*Sharded)(nil)
)
