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
	history := []string{c.root}

	for {
		path := history[len(history)-1]
		items, err := c.getList(path)
		if err != nil {
			return "", err
		}

		if len(items) == 0 {
			history = history[:len(path)-1]
			if len(history) == 0 {
				return "", ErrEmptyDir
			}
			continue
		}

		item := items[rand.Intn(len(items))]
		if !item.isDir {
			return item.path, nil
		}
	}
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

var ErrEmptyDir = errors.New("empty directory")
