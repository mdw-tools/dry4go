package dry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type Options struct {
	Paths     []string
	Threshold float64
	MinLines  int
	MinNodes  int
	Format    string
	Help      bool
}

type Location struct {
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

type Candidate struct {
	Score      float64  `json:"score"`
	Left       Location `json:"left"`
	Right      Location `json:"right"`
	LeftNodes  int      `json:"left_nodes"`
	RightNodes int      `json:"right_nodes"`
}

type entry struct {
	file         string
	startLine    int
	endLine      int
	nodes        int
	fingerprints map[string]bool
}

type node struct {
	Tag      string
	Children []node
}

var DefaultOptions = Options{
	Paths:     []string{"."},
	Threshold: 0.82,
	MinLines:  4,
	MinNodes:  20,
	Format:    "text",
}

const Usage = `Usage: dry4go [options] [file-or-directory ...]

Options:
  --threshold N   Minimum structural similarity score, default 0.82
  --min-lines N   Minimum source lines in a candidate function, default 4
  --min-nodes N   Minimum normalized syntax nodes, default 20
  --format F      text or json, default text
  --json          Same as --format json
  --text          Same as --format text`

func ParseArgs(args []string) (Options, error) {
	options := DefaultOptions
	options.Paths = nil
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--help", "-h":
			options.Help = true
			return options, nil
		case "--threshold", "--min-lines", "--min-nodes", "--format":
			if i+1 >= len(args) {
				return options, fmt.Errorf("missing value for %s", arg)
			}
			i++
			if err := applyValueOption(&options, arg, args[i]); err != nil {
				return options, err
			}
		case "--json":
			options.Format = "json"
		case "--text":
			options.Format = "text"
		default:
			if strings.HasPrefix(arg, "--") {
				return options, fmt.Errorf("unknown option: %s", arg)
			}
			options.Paths = append(options.Paths, arg)
		}
	}
	if len(options.Paths) == 0 {
		options.Paths = DefaultOptions.Paths
	}
	return options, nil
}

func FindDuplicates(options Options) ([]Candidate, error) {
	if len(options.Paths) == 0 {
		options.Paths = DefaultOptions.Paths
	}
	entries, err := scanPaths(options.Paths, options.MinLines, options.MinNodes)
	if err != nil {
		return nil, err
	}
	var candidates []Candidate
	for i := 0; i < len(entries); i++ {
		for j := i + 1; j < len(entries); j++ {
			score := similarity(entries[i], entries[j])
			if score >= options.Threshold {
				candidates = append(candidates, Candidate{
					Score:      score,
					Left:       location(entries[i]),
					Right:      location(entries[j]),
					LeftNodes:  entries[i].nodes,
					RightNodes: entries[j].nodes,
				})
			}
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.Left.File != b.Left.File {
			return a.Left.File < b.Left.File
		}
		if a.Left.StartLine != b.Left.StartLine {
			return a.Left.StartLine < b.Left.StartLine
		}
		if a.Right.File != b.Right.File {
			return a.Right.File < b.Right.File
		}
		return a.Right.StartLine < b.Right.StartLine
	})
	return candidates, nil
}

func FormatText(candidates []Candidate) string {
	if len(candidates) == 0 {
		return "No duplicate candidates found.\n"
	}
	var blocks []string
	for _, candidate := range candidates {
		blocks = append(blocks, fmt.Sprintf("DUPLICATE score=%.2f\n  %s\n  %s",
			candidate.Score, lineRange(candidate.Left), lineRange(candidate.Right)))
	}
	return strings.Join(blocks, "\n\n") + "\n"
}

func FormatJSON(candidates []Candidate) (string, error) {
	out := struct {
		Candidates []Candidate `json:"candidates"`
	}{Candidates: candidates}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}

func applyValueOption(options *Options, arg, value string) error {
	switch arg {
	case "--threshold":
		n, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return err
		}
		options.Threshold = n
	case "--min-lines":
		n, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		options.MinLines = n
	case "--min-nodes":
		n, err := strconv.Atoi(value)
		if err != nil {
			return err
		}
		options.MinNodes = n
	case "--format":
		if value != "text" && value != "json" {
			return fmt.Errorf("unknown format: %s", value)
		}
		options.Format = value
	}
	return nil
}

func scanPaths(paths []string, minLines, minNodes int) ([]entry, error) {
	files, err := filesForPaths(paths)
	if err != nil {
		return nil, err
	}
	var entries []entry
	for _, file := range files {
		fileEntries, err := scanFile(file, minLines, minNodes)
		if err != nil {
			return nil, err
		}
		entries = append(entries, fileEntries...)
	}
	return entries, nil
}

func filesForPaths(paths []string) ([]string, error) {
	seen := map[string]bool{}
	var files []string
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			if sourceFile(path, info) && !seen[path] {
				seen[path] = true
				files = append(files, filepath.ToSlash(path))
			}
			continue
		}
		err = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case ".git", "vendor", "target":
					return filepath.SkipDir
				}
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			p = filepath.ToSlash(p)
			if sourceFile(p, info) && !seen[p] {
				seen[p] = true
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(files)
	return files, nil
}

func sourceFile(path string, info os.FileInfo) bool {
	return !info.IsDir() && strings.HasSuffix(path, ".go")
}

func scanFile(path string, minLines, minNodes int) ([]entry, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	var entries []entry
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		start := fset.Position(fn.Pos()).Line
		end := fset.Position(fn.End()).Line
		normalized := normalizeFunc(fn)
		nodes := nodeCount(normalized)
		if end-start+1 < minLines || nodes < minNodes {
			continue
		}
		entries = append(entries, entry{
			file:         path,
			startLine:    start,
			endLine:      end,
			nodes:        nodes,
			fingerprints: fingerprints(normalized),
		})
	}
	return entries, nil
}

func normalizeFunc(fn *ast.FuncDecl) node {
	children := []node{normalizeFieldList("params", fn.Type.Params), normalizeFieldList("results", fn.Type.Results)}
	if fn.Recv != nil {
		children = append(children, normalizeFieldList("receiver", fn.Recv))
	}
	children = append(children, normalizeNode(fn.Body))
	return node{Tag: "func", Children: children}
}

func normalizeFieldList(tag string, fields *ast.FieldList) node {
	if fields == nil {
		return node{Tag: tag}
	}
	children := make([]node, 0, len(fields.List))
	for _, field := range fields.List {
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for i := 0; i < count; i++ {
			children = append(children, node{Tag: "field", Children: []node{normalizeNode(field.Type)}})
		}
	}
	return node{Tag: tag, Children: children}
}

func normalizeNode(n ast.Node) node {
	switch x := n.(type) {
	case nil:
		return node{Tag: "nil"}
	case *ast.BlockStmt:
		return normalizeList("block", stmtNodes(x.List))
	case *ast.IfStmt:
		return node{Tag: "if", Children: []node{normalizeNode(x.Init), normalizeNode(x.Cond), normalizeNode(x.Body), normalizeNode(x.Else)}}
	case *ast.ForStmt:
		return node{Tag: "for", Children: []node{normalizeNode(x.Init), normalizeNode(x.Cond), normalizeNode(x.Post), normalizeNode(x.Body)}}
	case *ast.RangeStmt:
		return node{Tag: "range", Children: []node{normalizeNode(x.X), normalizeNode(x.Body)}}
	case *ast.SwitchStmt:
		return node{Tag: "switch", Children: []node{normalizeNode(x.Init), normalizeNode(x.Tag), normalizeNode(x.Body)}}
	case *ast.TypeSwitchStmt:
		return node{Tag: "type-switch", Children: []node{normalizeNode(x.Init), normalizeNode(x.Assign), normalizeNode(x.Body)}}
	case *ast.SelectStmt:
		return node{Tag: "select", Children: []node{normalizeNode(x.Body)}}
	case *ast.CaseClause:
		return node{Tag: "case", Children: []node{normalizeList("case-list", exprNodes(x.List)), normalizeList("case-body", stmtNodes(x.Body))}}
	case *ast.CommClause:
		return node{Tag: "comm", Children: []node{normalizeNode(x.Comm), normalizeList("comm-body", stmtNodes(x.Body))}}
	case *ast.AssignStmt:
		return node{Tag: "assign/" + x.Tok.String(), Children: []node{normalizeList("lhs", exprNodes(x.Lhs)), normalizeList("rhs", exprNodes(x.Rhs))}}
	case *ast.DeclStmt:
		return node{Tag: "decl", Children: []node{normalizeDecl(x.Decl)}}
	case *ast.ExprStmt:
		return node{Tag: "expr-stmt", Children: []node{normalizeNode(x.X)}}
	case *ast.ReturnStmt:
		return normalizeList("return", exprNodes(x.Results))
	case *ast.BranchStmt:
		return node{Tag: "branch/" + x.Tok.String()}
	case *ast.GoStmt:
		return node{Tag: "go", Children: []node{normalizeNode(x.Call)}}
	case *ast.DeferStmt:
		return node{Tag: "defer", Children: []node{normalizeNode(x.Call)}}
	case *ast.SendStmt:
		return node{Tag: "send", Children: []node{normalizeNode(x.Chan), normalizeNode(x.Value)}}
	case *ast.IncDecStmt:
		return node{Tag: "incdec/" + x.Tok.String(), Children: []node{normalizeNode(x.X)}}
	case *ast.LabeledStmt:
		return node{Tag: "label", Children: []node{normalizeNode(x.Stmt)}}
	case *ast.EmptyStmt:
		return node{Tag: "empty"}
	case *ast.BadStmt:
		return node{Tag: "bad-stmt"}
	case *ast.BinaryExpr:
		return node{Tag: "binary/" + x.Op.String(), Children: []node{normalizeNode(x.X), normalizeNode(x.Y)}}
	case *ast.UnaryExpr:
		return node{Tag: "unary/" + x.Op.String(), Children: []node{normalizeNode(x.X)}}
	case *ast.CallExpr:
		return node{Tag: "call", Children: append([]node{normalizeCallee(x.Fun)}, exprNodes(x.Args)...)}
	case *ast.SelectorExpr:
		return node{Tag: "selector", Children: []node{normalizeNode(x.X), {Tag: "member"}}}
	case *ast.IndexExpr:
		return node{Tag: "index", Children: []node{normalizeNode(x.X), normalizeNode(x.Index)}}
	case *ast.IndexListExpr:
		return node{Tag: "index-list", Children: append([]node{normalizeNode(x.X)}, exprNodes(x.Indices)...)}
	case *ast.SliceExpr:
		return node{Tag: "slice", Children: []node{normalizeNode(x.X), normalizeNode(x.Low), normalizeNode(x.High), normalizeNode(x.Max)}}
	case *ast.StarExpr:
		return node{Tag: "star", Children: []node{normalizeNode(x.X)}}
	case *ast.ParenExpr:
		return node{Tag: "paren", Children: []node{normalizeNode(x.X)}}
	case *ast.CompositeLit:
		return node{Tag: "composite", Children: append([]node{normalizeNode(x.Type)}, exprNodes(x.Elts)...)}
	case *ast.KeyValueExpr:
		return node{Tag: "key-value", Children: []node{normalizeNode(x.Key), normalizeNode(x.Value)}}
	case *ast.FuncLit:
		return node{Tag: "func-lit", Children: []node{normalizeFieldList("params", x.Type.Params), normalizeFieldList("results", x.Type.Results), normalizeNode(x.Body)}}
	case *ast.TypeAssertExpr:
		return node{Tag: "type-assert", Children: []node{normalizeNode(x.X), normalizeNode(x.Type)}}
	case *ast.Ident:
		if x.Name == "true" || x.Name == "false" || x.Name == "nil" {
			return node{Tag: "literal/" + x.Name}
		}
		return node{Tag: "ident"}
	case *ast.BasicLit:
		return node{Tag: "literal/" + x.Kind.String()}
	case *ast.ArrayType:
		return node{Tag: "array-type", Children: []node{normalizeNode(x.Elt)}}
	case *ast.MapType:
		return node{Tag: "map-type", Children: []node{normalizeNode(x.Key), normalizeNode(x.Value)}}
	case *ast.StructType:
		return normalizeFieldList("struct-type", x.Fields)
	case *ast.InterfaceType:
		return normalizeFieldList("interface-type", x.Methods)
	case *ast.ChanType:
		return node{Tag: "chan-type", Children: []node{normalizeNode(x.Value)}}
	case *ast.FuncType:
		return node{Tag: "func-type", Children: []node{normalizeFieldList("params", x.Params), normalizeFieldList("results", x.Results)}}
	case *ast.Ellipsis:
		return node{Tag: "ellipsis", Children: []node{normalizeNode(x.Elt)}}
	default:
		return node{Tag: fmt.Sprintf("%T", n)}
	}
}

func normalizeDecl(decl ast.Decl) node {
	switch x := decl.(type) {
	case *ast.GenDecl:
		children := make([]node, 0, len(x.Specs))
		for _, spec := range x.Specs {
			children = append(children, normalizeSpec(spec))
		}
		return node{Tag: "gen-decl/" + x.Tok.String(), Children: children}
	default:
		return node{Tag: "decl"}
	}
}

func normalizeSpec(spec ast.Spec) node {
	switch x := spec.(type) {
	case *ast.ValueSpec:
		return node{Tag: "value-spec", Children: append([]node{normalizeNode(x.Type)}, exprNodes(x.Values)...)}
	case *ast.TypeSpec:
		return node{Tag: "type-spec", Children: []node{normalizeNode(x.Type)}}
	default:
		return node{Tag: "spec"}
	}
}

func normalizeCallee(expr ast.Expr) node {
	switch x := expr.(type) {
	case *ast.Ident:
		return node{Tag: "callee"}
	case *ast.SelectorExpr:
		return node{Tag: "selector-callee", Children: []node{normalizeNode(x.X), {Tag: "member"}}}
	default:
		return normalizeNode(x)
	}
}

func stmtNodes(stmts []ast.Stmt) []node {
	out := make([]node, 0, len(stmts))
	for _, stmt := range stmts {
		out = append(out, normalizeNode(stmt))
	}
	return out
}

func exprNodes(exprs []ast.Expr) []node {
	out := make([]node, 0, len(exprs))
	for _, expr := range exprs {
		out = append(out, normalizeNode(expr))
	}
	return out
}

func normalizeList(tag string, children []node) node {
	return node{Tag: tag, Children: children}
}

func nodeCount(n node) int {
	total := 1
	for _, child := range n.Children {
		total += nodeCount(child)
	}
	return total
}

func fingerprints(n node) map[string]bool {
	out := map[string]bool{}
	var walk func(node)
	walk = func(current node) {
		out[serialize(current)] = true
		for _, child := range current.Children {
			walk(child)
		}
	}
	walk(n)
	return out
}

func serialize(n node) string {
	var b bytes.Buffer
	writeNode(&b, n)
	return b.String()
}

func writeNode(b *bytes.Buffer, n node) {
	b.WriteString("(")
	b.WriteString(n.Tag)
	for _, child := range n.Children {
		b.WriteByte(' ')
		writeNode(b, child)
	}
	b.WriteString(")")
}

func similarity(left, right entry) float64 {
	intersection := 0
	for fp := range left.fingerprints {
		if right.fingerprints[fp] {
			intersection++
		}
	}
	union := len(left.fingerprints)
	for fp := range right.fingerprints {
		if !left.fingerprints[fp] {
			union++
		}
	}
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func location(e entry) Location {
	return Location{File: e.file, StartLine: e.startLine, EndLine: e.endLine}
}

func lineRange(location Location) string {
	return fmt.Sprintf("%s:%d-%d", location.File, location.StartLine, location.EndLine)
}
