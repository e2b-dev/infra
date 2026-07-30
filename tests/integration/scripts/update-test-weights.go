//go:build ignore

// Command update-test-weights regenerates test-weights.tsv from the JUnit XML
// of a full integration run:
//
//	go run scripts/update-test-weights.go <dir-containing-junit-xml>
//
// Run it from tests/integration; it rewrites scripts/test-weights.tsv in place.
//
// The weights only balance CI shards (see shard-tests.sh), so they are
// approximate by design: per top-level test we keep the slowest observation
// across every compression config in the directory, since the slowest config
// is what sets the wall clock.
package main

import (
	"encoding/xml"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const header = `# package	test	seconds
# Slowest-of-all-compression-configs wall time per top-level test, used only to
# balance CI shards (see shard-tests.sh). Regenerate with: make -C tests/integration update-test-weights
`

type report struct {
	Suites []struct {
		Name  string `xml:"name,attr"`
		Cases []struct {
			Name string `xml:"name,attr"`
			Time string `xml:"time,attr"`
		} `xml:"testcase"`
	} `xml:"testsuite"`
}

type key struct{ pkg, test string }

func main() {
	log.SetFlags(0)
	if len(os.Args) != 2 {
		log.Fatalf("usage: %s <dir-containing-junit-xml>", filepath.Base(os.Args[0]))
	}

	weights, files, err := collect(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	if files == 0 {
		log.Fatalf("no JUnit XML found under %s", os.Args[1])
	}

	const out = "scripts/test-weights.tsv"
	if err := write(out, weights); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %d weights from %d file(s) to %s\n", len(weights), files, out)
}

func collect(dir string) (map[key]float64, int, error) {
	weights := map[key]float64{}
	files := 0

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".xml" {
			return err
		}
		files++

		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var parsed report
		if err := xml.Unmarshal(raw, &parsed); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}

		for _, suite := range parsed.Suites {
			_, pkg, _ := strings.Cut(suite.Name, "tests/integration/")
			times := make(map[string]float64, len(suite.Cases))
			for _, c := range suite.Cases {
				seconds, _ := strconv.ParseFloat(c.Time, 64)
				times[c.Name] = seconds
			}
			for name, own := range times {
				if strings.Contains(name, "/") {
					continue
				}
				// A parent with parallel subtests reports ~0s of its own, so
				// take whichever of the two accountings is larger.
				subtests := 0.0
				for sub, seconds := range times {
					if strings.HasPrefix(sub, name+"/") {
						subtests += seconds
					}
				}
				weights[key{pkg, name}] = max(weights[key{pkg, name}], own, subtests)
			}
		}
		return nil
	})

	return weights, files, err
}

func write(path string, weights map[key]float64) error {
	keys := make([]key, 0, len(weights))
	for k := range weights {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].pkg != keys[j].pkg {
			return keys[i].pkg < keys[j].pkg
		}
		return keys[i].test < keys[j].test
	})

	var b strings.Builder
	b.WriteString(header)
	for _, k := range keys {
		fmt.Fprintf(&b, "%s\t%s\t%.1f\n", k.pkg, k.test, weights[k])
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
