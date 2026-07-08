package dry

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
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

func TestFindDuplicatesClassifiesShapeTwins(t *testing.T) {
	dir := t.TempDir()
	writeSource(t, dir, "left.go", `package sample

func DupLeft(xs []int) []int {
	var out []int
	for _, x := range xs {
		if x%2 == 1 {
			out = append(out, x*3)
		}
	}
	return out
}

func TwinLeft() string {
	a := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	b := "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	if a == b {
		return "left samples never match"
	}
	return a + b
}
`)
	writeSource(t, dir, "right.go", `package sample

func DupRight(items []int) []int {
	var kept []int
	for _, item := range items {
		if item%2 == 1 {
			kept = append(kept, item*3)
		}
	}
	return kept
}

func TwinRight() string {
	c := "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC"
	d := "DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD"
	if c == d {
		return "right samples never align"
	}
	return c + d
}
`)
	candidates, err := FindDuplicates(Options{Paths: []string{dir}, Threshold: 0.82, MinLines: 4, MinNodes: 8})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected a duplicate and a shape twin, got %#v", candidates)
	}
	duplicate, twin := candidates[0], candidates[1]
	if duplicate.Kind != KindDuplicate || duplicate.Score < 0.82 {
		t.Fatalf("expected leading duplicate candidate, got %#v", duplicate)
	}
	if twin.Kind != KindShapeTwin {
		t.Fatalf("expected trailing shape-twin candidate, got %#v", twin)
	}
	if twin.StructuralScore < 0.95 {
		t.Fatalf("expected near-identical structure, got %#v", twin)
	}
	if twin.Score >= 0.82 {
		t.Fatalf("expected combined score below threshold, got %#v", twin)
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

func TestLiteralFeatures(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sample.go", `package sample

func Sample() {
	a := "hi"
	b := "hi"
	c := 42
	d := 1.5
	e := true
	var f any = nil
	g := "this string literal is definitely longer than fifty-six characters overall"
	_, _, _, _, _, _, _ = a, b, c, d, e, f, g
}
`, 0)
	if err != nil {
		t.Fatal(err)
	}
	features := literalFeatures(file.Decls[0].(*ast.FuncDecl))
	want := map[string]float64{
		`lit/STRING/"hi"`: 1.5,
		`lit/INT/42`:      1.25,
		`lit/FLOAT/1.5`:   1.375,
		`lit/STRING/"this string literal is definitely longer than fifty-six characters overall"`: 8,
	}
	if !reflect.DeepEqual(features, want) {
		t.Fatalf("expected %#v, got %#v", want, features)
	}
}

func entryForFunc(t *testing.T, body string) entry {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sample.go", "package sample\n\n"+body, 0)
	if err != nil {
		t.Fatal(err)
	}
	fn := file.Decls[0].(*ast.FuncDecl)
	normalized := normalizeFunc(fn)
	return entry{
		nodes:        nodeCount(normalized),
		fingerprints: fingerprints(normalized),
		literals:     literalFeatures(fn),
	}
}

func TestSimilarityIdenticalStructureAndLiterals(t *testing.T) {
	source := `func Same(xs []int) int {
	total := 0
	for _, x := range xs {
		total += x * 2
	}
	return total
}
`
	structural, combined := similarity(entryForFunc(t, source), entryForFunc(t, source))
	if structural != 1.0 || combined != 1.0 {
		t.Fatalf("expected perfect scores, got structural=%v combined=%v", structural, combined)
	}
}

func TestSimilarityDivergentHeavyLiteralsLowersCombinedScore(t *testing.T) {
	left := entryForFunc(t, `func L() string {
	a := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	b := "BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"
	return a + b
}
`)
	right := entryForFunc(t, `func R() string {
	c := "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC"
	d := "DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD"
	return c + d
}
`)
	structural, combined := similarity(left, right)
	if structural != 1.0 {
		t.Fatalf("expected identical structure, got %v", structural)
	}
	if combined >= 0.7 {
		t.Fatalf("expected combined score well below threshold, got %v", combined)
	}
}

func TestSimilarityOneSmallDivergentLiteralBarelyMoves(t *testing.T) {
	template := `func F(xs []int) int {
	total := 0
	count := 0
	for i, x := range xs {
		if i%3 == 0 && x > 10 {
			total += x * 2
			count++
		} else {
			total -= x / 4
		}
	}
	if total < 0 {
		total = 0
	}
	if count > 5 {
		return total / count
	}
	return total + %s
}
`
	left := entryForFunc(t, strings.ReplaceAll(template, "%s", "7"))
	right := entryForFunc(t, strings.ReplaceAll(template, "%s", "9"))
	structural, combined := similarity(left, right)
	if structural != 1.0 {
		t.Fatalf("expected identical structure, got %v", structural)
	}
	if combined < 0.95 {
		t.Fatalf("expected combined score >= 0.95, got %v", combined)
	}
}

func TestSimilarityWithoutLiteralsMatchesStructuralScore(t *testing.T) {
	left := entryForFunc(t, `func L(a, b int) int {
	if a > b {
		return a
	}
	return b
}
`)
	right := entryForFunc(t, `func R(a, b int) int {
	for a > b {
		a = a - b
	}
	return a
}
`)
	structural, combined := similarity(left, right)
	if structural <= 0 || structural >= 1 {
		t.Fatalf("expected partial structural similarity, got %v", structural)
	}
	if combined != structural {
		t.Fatalf("expected combined %v to equal structural %v", combined, structural)
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

func TestFormatTextShapeTwin(t *testing.T) {
	text := FormatText([]Candidate{{
		Kind:            KindShapeTwin,
		Score:           0.61,
		StructuralScore: 1.0,
		Left:            Location{File: "a.go", StartLine: 10, EndLine: 14},
		Right:           Location{File: "b.go", StartLine: 20, EndLine: 24},
	}})
	want := "SHAPE-TWIN structural=1.00 score=0.61 (consider table/parameterization)\n  a.go:10-14\n  b.go:20-24\n"
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

func TestFormatJSONIncludesKindAndStructuralScore(t *testing.T) {
	text, err := FormatJSON([]Candidate{{Kind: KindShapeTwin, Score: 0.5, StructuralScore: 0.96}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, `"kind": "shape-twin"`) {
		t.Fatalf("missing kind: %s", text)
	}
	if !strings.Contains(text, `"structural_score": 0.96`) {
		t.Fatalf("missing structural_score: %s", text)
	}
}
