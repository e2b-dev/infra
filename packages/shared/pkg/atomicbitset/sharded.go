package atomicbitset

import (
	"sync/atomic"

	"github.com/bits-and-blooms/bitset"
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

func (s *Sharded) BitSet() *bitset.BitSet {
	totalWords := (s.n + 63) / 64
	words := make([]uint64, totalWords)

	for si := range s.shards {
		sh := s.shards[si].Load()
		if sh == nil {
			continue
		}
		baseWord := uint(si) * s.bitsPerShard / 64
		for wi := range sh.words {
			dst := baseWord + uint(wi)
			if dst >= totalWords {
				break
			}
			words[dst] = sh.words[wi].Load()
		}
	}

	return bitset.FromWithLength(s.n, words)
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
	_ Bitset = (*Roaring64)(nil)
	_ Bitset = (*Sharded)(nil)
	_ Bitset = (*SyncMap)(nil)
)
