package internal_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// TestRoadmapRows: the roadmap's decision rows are numbered once each, in
// ascending order. Branches each append rows at the table's end, and git
// merges that file with the union driver (.gitattributes), which keeps both
// sides' lines: this catches two branches taking one number, and a row both
// edited, now there twice.
func TestRoadmapRows(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "docs", "superpowers", "plans", "2026-10-25-railswitch-roadmap.md"))
	if err != nil {
		t.Fatal(err)
	}
	prev := 0
	for _, m := range regexp.MustCompile(`(?m)^\| D(\d+) \|`).FindAllSubmatch(data, -1) {
		n, _ := strconv.Atoi(string(m[1]))
		switch {
		case n == prev:
			t.Errorf("D%d is there twice: two branches took it, or a merge kept two versions of the row", n)
		case n < prev:
			t.Errorf("D%d comes after D%d: rows go in number order", n, prev)
		}
		prev = n
	}
	if prev == 0 {
		t.Fatal("no | Dnnn | rows found: the roadmap's table moved?")
	}
}
