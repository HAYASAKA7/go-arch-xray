package analyzer

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestAnalyzeFuncComplexity_ControlFlowScores(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		cyclomatic int
		cognitive  int
		maxNesting int
	}{
		{
			name: "linear",
			src: `func sample() int {
	return 1
}`,
			cyclomatic: 1,
			cognitive:  0,
			maxNesting: 0,
		},
		{
			name: "if else",
			src: `func sample(ok bool) int {
	if ok {
		return 1
	}
	return 0
}`,
			cyclomatic: 2,
			cognitive:  1,
			maxNesting: 1,
		},
		{
			name: "logical operators",
			src: `func sample(a, b, c bool) int {
	if a && b || c {
		return 1
	}
	return 0
}`,
			cyclomatic: 4,
			cognitive:  3,
			maxNesting: 1,
		},
		{
			name: "switch cases",
			src: `func sample(x int) int {
	switch x {
	case 1:
		return 1
	case 2:
		return 2
	default:
		return 0
	}
}`,
			cyclomatic: 3,
			cognitive:  1,
			maxNesting: 1,
		},
		{
			name: "nested loop and if",
			src: `func sample(x int) int {
	for i := 0; i < x; i++ {
		if i%2 == 0 {
			return i
		}
	}
	return 0
}`,
			cyclomatic: 3,
			cognitive:  3,
			maxNesting: 2,
		},
		{
			name: "recursion",
			src: `func sample(x int) int {
	if x <= 1 {
		return 1
	}
	return x * sample(x-1)
}`,
			cyclomatic: 2,
			cognitive:  2,
			maxNesting: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fd := parseSingleFunc(t, tt.src)
			cyclomatic, cognitive, maxNesting := analyzeFuncComplexity(fd)
			if cyclomatic != tt.cyclomatic || cognitive != tt.cognitive || maxNesting != tt.maxNesting {
				t.Fatalf("got cc=%d cognitive=%d nesting=%d, want cc=%d cognitive=%d nesting=%d", cyclomatic, cognitive, maxNesting, tt.cyclomatic, tt.cognitive, tt.maxNesting)
			}
		})
	}
}

func TestComputeComplexityMetrics_FiltersSortsAndSummarizes(t *testing.T) {
	dir := createDependencyTestModule(t, "complexity_basic", map[string]string{
		"main.go": `package main

func main() { simple(); complex(3) }

func simple() int {
	return 1
}

func complex(x int) int {
	total := 0
	for i := 0; i < x; i++ {
		if i%2 == 0 || i%3 == 0 {
			total += i
		}
	}
	return total
}
`,
	})
	ws := NewWorkspace()
	result, err := ComputeComplexityMetricsWithOptions(ws, dir, "./...", ComplexityOptions{
		MinCognitive:    2,
		IncludePackages: true,
	}, QueryOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 1 {
		t.Fatalf("expected one function after min_cognitive filter, got %+v", result)
	}
	if result.Functions[0].Name != "complex" {
		t.Fatalf("expected complex function first, got %+v", result.Functions)
	}
	if result.Functions[0].Cyclomatic != 4 || result.Functions[0].Cognitive != 4 {
		t.Fatalf("unexpected complex scores: %+v", result.Functions[0])
	}
	if len(result.Packages) != 1 {
		t.Fatalf("expected one package aggregate, got %+v", result.Packages)
	}
	if result.Packages[0].FunctionCount != 3 {
		t.Fatalf("package aggregate should include all functions before filters, got %+v", result.Packages[0])
	}
}

func TestComputeComplexityMetrics_PackageAggregateTracksIndependentMaxima(t *testing.T) {
	dir := createDependencyTestModule(t, "complexity_aggregate_max", map[string]string{
		"main.go": `package main

func main() { wide(1); deep(1) }

func wide(x int) int {
	switch x {
	case 1:
		return 1
	case 2:
		return 2
	case 3:
		return 3
	default:
		return 0
	}
}

func deep(x int) int {
	for i := 0; i < x; i++ {
		if i%2 == 0 {
			return i
		}
	}
	return 0
}
`,
	})
	ws := NewWorkspace()
	result, err := ComputeComplexityMetricsWithOptions(ws, dir, "./...", ComplexityOptions{IncludePackages: true}, QueryOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Packages) != 1 {
		t.Fatalf("expected one package aggregate, got %+v", result.Packages)
	}
	agg := result.Packages[0]
	if agg.MaxCyclomatic != 4 {
		t.Fatalf("expected max cyclomatic from wide switch to be 4, got %+v", agg)
	}
	if agg.MaxCognitive != 3 {
		t.Fatalf("expected max cognitive from deep nesting to be 3, got %+v", agg)
	}
}

func TestComputeComplexityMetrics_Streaming(t *testing.T) {
	dir := createDependencyTestModule(t, "complexity_stream", map[string]string{
		"main.go": `package main

func main() { a(); b(); c() }
func a() { if true { return } }
func b() { if true { return } }
func c() { if true { return } }
`,
	})
	ws := NewWorkspace()
	result, err := ComputeComplexityMetricsWithOptions(ws, dir, "./...", ComplexityOptions{}, QueryOptions{ChunkSize: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Functions) != 2 || !result.HasMore || result.NextCursor == "" {
		t.Fatalf("expected first streamed chunk with cursor, got %+v", result)
	}

	next, err := ComputeComplexityMetricsWithOptions(ws, dir, "./...", ComplexityOptions{}, QueryOptions{ChunkSize: 2, Cursor: result.NextCursor})
	if err != nil {
		t.Fatalf("unexpected error fetching next chunk: %v", err)
	}
	if len(next.Functions) == 0 || next.HasMore {
		t.Fatalf("expected final streamed chunk, got %+v", next)
	}
}

func parseSingleFunc(t *testing.T, src string) *ast.FuncDecl {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sample.go", "package sample\n\n"+src, 0)
	if err != nil {
		t.Fatalf("parse snippet: %v", err)
	}
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if ok {
			return fd
		}
	}
	t.Fatal("no function declaration found")
	return nil
}
