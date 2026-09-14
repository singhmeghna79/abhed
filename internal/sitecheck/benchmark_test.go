package sitecheck

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"testing"
)

// TestBenchmarkFiguresMatchResults pins every benchmark number on the site to
// the per-run result files under bench/results. A benchmark figure is the
// easiest number on a marketing page to leave stale or round generously, and
// this one is Abhed's own claim about its own harness, so it gets the same
// treatment as the test count: recomputed from the source of truth on every
// run, never trusted from the page.
//
// The page carries the figure as
//
//	<b data-bench="abhed">17/24</b>
//
// for each system (abhed, aider, bare). A page without a benchmark section is
// not checked; a page with one is checked against the latest results date.
func TestBenchmarkFiguresMatchResults(t *testing.T) {
	src := raw(t)
	fig := regexp.MustCompile(`<b data-bench="([a-z]+)">\s*(\d+)\s*/\s*(\d+)\s*</b>`)
	claims := fig.FindAllStringSubmatch(src, -1)
	if len(claims) == 0 {
		return // no benchmark figures on this page
	}

	root := filepath.Join("..", "..", "bench", "results")
	dates, err := os.ReadDir(root)
	if err != nil || len(dates) == 0 {
		t.Fatalf("the page carries benchmark figures but bench/results has no runs: %v", err)
	}
	names := []string{}
	for _, d := range dates {
		if d.IsDir() {
			names = append(names, d.Name())
		}
	}
	sort.Strings(names)
	latest := filepath.Join(root, names[len(names)-1])

	count := func(system string) (passed, total int) {
		files, _ := filepath.Glob(filepath.Join(latest, system, "*.json"))
		for _, f := range files {
			var r struct {
				Score struct {
					Pass bool `json:"pass"`
				} `json:"score"`
			}
			b, err := os.ReadFile(f)
			if err != nil || json.Unmarshal(b, &r) != nil {
				continue
			}
			total++
			if r.Score.Pass {
				passed++
			}
		}
		return
	}

	for _, c := range claims {
		system := c[1]
		gotPass, _ := strconv.Atoi(c[2])
		gotTotal, _ := strconv.Atoi(c[3])
		passed, total := count(system)
		if total == 0 {
			t.Errorf("the page claims %s/%s for %q but %s has no result files for it", c[2], c[3], system, latest)
			continue
		}
		if gotPass != passed || gotTotal != total {
			t.Errorf("the page says %s passed %d/%d; the results in %s say %d/%d — update the page from bench/RESULTS.md, never by hand",
				system, gotPass, gotTotal, filepath.Base(latest), passed, total)
		}
	}
}
