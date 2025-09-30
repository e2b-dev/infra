package pkg

import (
	"errors"
	"math/rand"
	"os"
	"path/filepath"
)

type ListingCache struct {
	root  string
	cache map[string][]cacheEntry
}

type cacheEntry struct {
	path  string
	isDir bool
}

func NewListingCache(root string) *ListingCache {
	return &ListingCache{
		root:  root,
		cache: make(map[string][]cacheEntry),
	}
}

func (c *ListingCache) Decache(path string) {
	dirName := filepath.Dir(path)
	delete(c.cache, dirName)
}

func (c *ListingCache) GetRandomFile() (string, error) {
	return c.getRandomFile(c.root)
}

func (c *ListingCache) getRandomFile(path string) (string, error) {
	items, err := c.getList(path)
	if err != nil {
		return "", err
	}

	rand.Shuffle(len(items), func(i, j int) {
		items[i], items[j] = items[j], items[i]
	})

	for _, item := range items {
		if !item.isDir {
			return item.path, nil
		}

		path, err := c.getRandomFile(item.path)
		if err == nil {
			return path, nil
		}

		continue
	}

	return "", ErrNoFiles
}

func (c *ListingCache) getList(path string) ([]cacheEntry, error) {
	cached, ok := c.cache[path]
	if ok {
		return cached, nil
	}

	items, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	var entries []cacheEntry
	for _, item := range items {
		entries = append(entries, cacheEntry{
			path:  filepath.Join(path, item.Name()),
			isDir: item.IsDir(),
		})
	}

	c.cache[path] = entries

	return entries, nil
}

var ErrNoFiles = errors.New("no files")
