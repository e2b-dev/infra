package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"sort"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const pageSize = int(header.PageSize)

type scenario struct {
	name      string
	buildID   uuid.UUID
	parentID  uuid.UUID
	siblingID uuid.UUID
	mutate    func(*corpus, []int)
}

type corpus struct {
	pages int

	parentMem   []byte
	parentRoot  []byte
	childMem    []byte
	childRoot   []byte
	siblingMem  []byte
	siblingRoot []byte
}

func main() {
	out := flag.String("out", ".synthetic-dedup", "output storage root")
	pages := flag.Int("pages", 2048, "pages per artifact")
	dirtyPages := flag.Int("dirty-pages", 512, "dirty pages per child artifact")
	seed := flag.Int64("seed", 1, "random seed")
	flag.Parse()

	if *dirtyPages > *pages {
		log.Fatal("-dirty-pages cannot exceed -pages")
	}
	if err := os.RemoveAll(*out); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(*out, "templates"), 0o755); err != nil {
		log.Fatal(err)
	}

	rng := rand.New(rand.NewSource(*seed))
	pairsPath := filepath.Join(*out, "pairs.csv")
	pairs, err := os.Create(pairsPath)
	if err != nil {
		log.Fatal(err)
	}
	defer pairs.Close()
	pairsCSV := csv.NewWriter(pairs)
	if err := pairsCSV.Write([]string{"build_id", "parent_build_id", "sibling_build_id", "family"}); err != nil {
		log.Fatal(err)
	}

	for _, s := range scenarios() {
		dirty := pickPages(*pages, *dirtyPages, rng)
		c := newCorpus(*pages, rng)
		s.mutate(c, dirty)
		if err := writeScenario(*out, s, c, dirty); err != nil {
			log.Fatal(err)
		}
		if err := pairsCSV.Write([]string{s.buildID.String(), s.parentID.String(), s.siblingID.String(), s.name}); err != nil {
			log.Fatal(err)
		}
	}
	pairsCSV.Flush()
	if err := pairsCSV.Error(); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("storage=%s\npairs=%s\n", *out, pairsPath)
}

func scenarios() []scenario {
	return []scenario{
		{
			name:      "writeback_current_rootfs",
			buildID:   uuid.MustParse("10000000-0000-0000-0000-000000000001"),
			parentID:  uuid.MustParse("10000000-0000-0000-0000-000000000002"),
			siblingID: uuid.MustParse("10000000-0000-0000-0000-000000000003"),
			mutate: func(c *corpus, dirty []int) {
				for i, p := range dirty {
					fillPage(c.childRoot, p, byte(40+i%100))
					copyPage(c.childMem, p, c.childRoot, p)
				}
			},
		},
		{
			name:      "rootfs_from_parent_memfile",
			buildID:   uuid.MustParse("20000000-0000-0000-0000-000000000001"),
			parentID:  uuid.MustParse("20000000-0000-0000-0000-000000000002"),
			siblingID: uuid.MustParse("20000000-0000-0000-0000-000000000003"),
			mutate: func(c *corpus, dirty []int) {
				for _, p := range dirty {
					copyPage(c.childRoot, p, c.parentMem, p)
					fillPage(c.childMem, p, 0x91)
				}
			},
		},
		{
			name:      "sibling_memfile",
			buildID:   uuid.MustParse("30000000-0000-0000-0000-000000000001"),
			parentID:  uuid.MustParse("30000000-0000-0000-0000-000000000002"),
			siblingID: uuid.MustParse("30000000-0000-0000-0000-000000000003"),
			mutate: func(c *corpus, dirty []int) {
				for _, p := range dirty {
					copyPage(c.childMem, p, c.siblingMem, p)
					fillPage(c.childRoot, p, 0xa1)
				}
			},
		},
		{
			name:      "parent_rootfs_only",
			buildID:   uuid.MustParse("40000000-0000-0000-0000-000000000001"),
			parentID:  uuid.MustParse("40000000-0000-0000-0000-000000000002"),
			siblingID: uuid.MustParse("40000000-0000-0000-0000-000000000003"),
			mutate: func(c *corpus, dirty []int) {
				for _, p := range dirty {
					copyPage(c.childMem, p, c.parentRoot, p)
					fillPage(c.childRoot, p, 0xb1)
				}
			},
		},
		{
			name:      "random",
			buildID:   uuid.MustParse("50000000-0000-0000-0000-000000000001"),
			parentID:  uuid.MustParse("50000000-0000-0000-0000-000000000002"),
			siblingID: uuid.MustParse("50000000-0000-0000-0000-000000000003"),
			mutate: func(c *corpus, dirty []int) {
				for i, p := range dirty {
					fillPage(c.childMem, p, byte(0xc0+i%31))
					fillPage(c.childRoot, p, byte(0xe0+i%31))
				}
			},
		},
	}
}

func newCorpus(pages int, rng *rand.Rand) *corpus {
	size := pages * pageSize
	c := &corpus{
		pages:       pages,
		parentMem:   make([]byte, size),
		parentRoot:  make([]byte, size),
		childMem:    make([]byte, size),
		childRoot:   make([]byte, size),
		siblingMem:  make([]byte, size),
		siblingRoot: make([]byte, size),
	}
	fillRandom(c.parentMem, rng)
	fillRandom(c.parentRoot, rng)
	fillRandom(c.siblingMem, rng)
	fillRandom(c.siblingRoot, rng)
	copy(c.childMem, c.parentMem)
	copy(c.childRoot, c.parentRoot)

	return c
}

func writeScenario(root string, s scenario, c *corpus, dirty []int) error {
	if err := writeFullBuild(root, s.parentID, c.parentMem, c.parentRoot); err != nil {
		return err
	}
	if err := writeFullBuild(root, s.siblingID, c.siblingMem, c.siblingRoot); err != nil {
		return err
	}
	if err := writeChildBuild(root, s.buildID, s.parentID, c, dirty); err != nil {
		return err
	}

	return nil
}

func writeFullBuild(root string, id uuid.UUID, mem, rootfs []byte) error {
	if err := writeArtifact(root, id, id, storage.MemfileName, mem, nil); err != nil {
		return err
	}
	return writeArtifact(root, id, id, storage.RootfsName, rootfs, nil)
}

func writeChildBuild(root string, id, parent uuid.UUID, c *corpus, dirty []int) error {
	if err := writeArtifact(root, id, parent, storage.MemfileName, c.childMem, dirty); err != nil {
		return err
	}
	return writeArtifact(root, id, parent, storage.RootfsName, c.childRoot, dirty)
}

func writeArtifact(root string, id, parent uuid.UUID, name string, data []byte, dirty []int) error {
	dir := filepath.Join(root, "templates", id.String())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	var mappings []header.BuildMap
	var object []byte
	if dirty == nil {
		object = data
		mappings = []header.BuildMap{{
			Offset:             0,
			Length:             uint64(len(data)),
			BuildId:            id,
			BuildStorageOffset: 0,
		}}
	} else {
		sort.Ints(dirty)
		dirtyMap := roaring.New()
		for _, p := range dirty {
			dirtyMap.Add(uint32(p))
			start := p * pageSize
			object = append(object, data[start:start+pageSize]...)
		}
		parentMapping := []header.BuildMap{{
			Offset:             0,
			Length:             uint64(len(data)),
			BuildId:            parent,
			BuildStorageOffset: 0,
		}}
		selfMapping := header.CreateMapping(&id, dirtyMap, header.PageSize)
		mappings = header.NormalizeMappings(header.MergeMappings(parentMapping, selfMapping))
	}

	if err := os.WriteFile(filepath.Join(dir, name), object, 0o644); err != nil {
		return err
	}
	h, err := header.NewHeader(&header.Metadata{
		Version:     3,
		BlockSize:   uint64(header.PageSize),
		Size:        uint64(len(data)),
		Generation:  1,
		BuildId:     id,
		BaseBuildId: parent,
	}, mappings)
	if err != nil {
		return err
	}
	raw, err := header.SerializeHeader(h)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name+storage.HeaderSuffix), raw, 0o644)
}

func pickPages(pages, n int, rng *rand.Rand) []int {
	perm := rng.Perm(pages)
	return perm[:n]
}

func fillRandom(b []byte, rng *rand.Rand) {
	for i := range b {
		b[i] = byte(rng.Intn(255) + 1)
	}
}

func fillPage(b []byte, page int, value byte) {
	start := page * pageSize
	for i := start; i < start+pageSize; i++ {
		b[i] = value
	}
}

func copyPage(dst []byte, dstPage int, src []byte, srcPage int) {
	copy(dst[dstPage*pageSize:(dstPage+1)*pageSize], src[srcPage*pageSize:(srcPage+1)*pageSize])
}
