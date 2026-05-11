package dry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSource(t *testing.T, dir, name, text string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFindDuplicatesReportsStructuralMatches(t *testing.T) {
	dir := t.TempDir()
	left := writeSource(t, dir, "left.go", `package sample

func Alpha(xs []int) []int {
	var ys []int
	for _, x := range xs {
		if x%2 == 1 {
			ys = append(ys, x+1)
		}
	}
	return ys
}
`)
	right := writeSource(t, dir, "right.go", `package sample

func Beta(items []int) []int {
	var kept []int
	for _, item := range items {
		if item%2 == 0 {
			kept = append(kept, item+1)
		}
	}
	return kept
}
`)
	candidates, err := FindDuplicates(Options{Paths: []string{dir}, Threshold: 0.80, MinLines: 4, MinNodes: 8})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected one candidate, got %#v", candidates)
	}
	if candidates[0].Left.File != filepath.ToSlash(left) || candidates[0].Right.File != filepath.ToSlash(right) {
		t.Fatalf("unexpected files: %#v", candidates[0])
	}
	if candidates[0].Left.StartLine != 3 || candidates[0].Left.EndLine != 11 {
		t.Fatalf("unexpected line range: %#v", candidates[0].Left)
	}
}

func TestFindDuplicatesMatchesMethodsAndCompositeLiterals(t *testing.T) {
	dir := t.TempDir()
	writeSource(t, dir, "left.go", `package sample

type Invoice struct{}

func (Invoice) Summary(rows []Row) map[string]int {
	out := map[string]int{}
	for _, row := range rows {
		if row.Paid && row.Total > 0 {
			out[row.ID] = row.Total
		}
	}
	return out
}
`)
	writeSource(t, dir, "right.go", `package sample

type Receipt struct{}

func (Receipt) Report(items []Row) map[string]int {
	found := map[string]int{}
	for _, item := range items {
		if item.Closed && item.Amount > 0 {
			found[item.Key] = item.Amount
		}
	}
	return found
}
`)
	candidates, err := FindDuplicates(Options{Paths: []string{dir}, Threshold: 0.75, MinLines: 4, MinNodes: 8})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("expected one candidate, got %#v", candidates)
	}
}

func TestFiltersShortFunctions(t *testing.T) {
	dir := t.TempDir()
	writeSource(t, dir, "one.go", "package sample\nfunc A(x int) int { return x + 1 }\n")
	writeSource(t, dir, "two.go", "package sample\nfunc B(y int) int { return y + 2 }\n")
	candidates, err := FindDuplicates(Options{Paths: []string{dir}, Threshold: 0.8, MinLines: 3, MinNodes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 0 {
		t.Fatalf("expected no candidates, got %#v", candidates)
	}
}

func TestParseArgs(t *testing.T) {
	options, err := ParseArgs([]string{"--threshold", "0.9", "--min-lines", "5", "--min-nodes", "30", "--json", "pkg"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Threshold != 0.9 || options.MinLines != 5 || options.MinNodes != 30 || options.Format != "json" {
		t.Fatalf("unexpected options: %#v", options)
	}
	if len(options.Paths) != 1 || options.Paths[0] != "pkg" {
		t.Fatalf("unexpected paths: %#v", options.Paths)
	}
}

func TestParseArgsDefaultsToCurrentDirectory(t *testing.T) {
	options, err := ParseArgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(options.Paths) != 1 || options.Paths[0] != "." {
		t.Fatalf("unexpected default paths: %#v", options.Paths)
	}
}

func TestFormatText(t *testing.T) {
	text := FormatText([]Candidate{{
		Score: 0.875,
		Left:  Location{File: "a.go", StartLine: 10, EndLine: 14},
		Right: Location{File: "b.go", StartLine: 20, EndLine: 24},
	}})
	want := "DUPLICATE score=0.88\n  a.go:10-14\n  b.go:20-24\n"
	if text != want {
		t.Fatalf("expected %q, got %q", want, text)
	}
}

func TestFormatTextNoCandidates(t *testing.T) {
	text := FormatText(nil)
	if text != "No duplicate candidates found.\n" {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestFormatJSON(t *testing.T) {
	text, err := FormatJSON([]Candidate{{Score: 1.0}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, `"candidates"`) {
		t.Fatalf("missing candidates: %s", text)
	}
	var parsed struct {
		Candidates []Candidate `json:"candidates"`
	}
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Candidates) != 1 {
		t.Fatalf("unexpected json: %#v", parsed)
	}
}
