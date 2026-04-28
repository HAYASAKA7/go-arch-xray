package analyzer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAnalyzeCallHierarchy_StaticCallsMaxDepthThree(t *testing.T) {
	dir := createCallHierarchyTestModule(t, "callstatic", map[string]string{
		"main.go": `package main

func Root() { A() }
func A() { B() }
func B() { C() }
func C() { D() }
func D() {}
`,
	})

	ws := NewWorkspace()
	result, err := AnalyzeCallHierarchy(ws, dir, "./...", "Root", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.RootFunction == "" {
		t.Fatal("expected root function")
	}
	if result.MaxDepth != 3 {
		t.Fatalf("expected max depth 3, got %d", result.MaxDepth)
	}
	if !hasCallEdge(result, "Root", "A", "Static") {
		t.Fatal("missing Root -> A static edge")
	}
	if !hasCallEdge(result, "A", "B", "Static") {
		t.Fatal("missing A -> B static edge")
	}
	if !hasCallEdge(result, "B", "C", "Static") {
		t.Fatal("missing B -> C static edge")
	}
	if hasCallEdge(result, "C", "D", "Static") {
		t.Fatal("did not expect C -> D beyond max depth")
	}
}

func TestAnalyzeCallHierarchy_DeduplicatesRecursiveCycles(t *testing.T) {
	dir := createCallHierarchyTestModule(t, "callrecursive", map[string]string{
		"main.go": `package main

func Root() { Loop() }
func Loop() { Loop() }
`,
	})

	ws := NewWorkspace()
	result, err := AnalyzeCallHierarchy(ws, dir, "./...", "Root", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	seen := map[string]bool{}
	for _, edge := range result.Edges {
		key := edge.Caller + "->" + edge.Callee
		if seen[key] {
			t.Fatalf("duplicate recursive edge %s", key)
		}
		seen[key] = true
	}
}

func TestAnalyzeCallHierarchy_LabelsGoroutineEdges(t *testing.T) {
	dir := createCallHierarchyTestModule(t, "callgo", map[string]string{
		"main.go": `package main

func Root() { go Worker() }
func Worker() {}
`,
	})

	ws := NewWorkspace()
	result, err := AnalyzeCallHierarchy(ws, dir, "./...", "Root", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !hasCallEdge(result, "Root", "Worker", "Goroutine") {
		t.Fatal("missing Root -> Worker goroutine edge")
	}
}

func hasCallEdge(r *CallHierarchyResult, caller, callee, callType string) bool {
	for _, edge := range r.Edges {
		if shortFuncName(edge.Caller) == caller && shortFuncName(edge.Callee) == callee && edge.CallType == callType {
			return true
		}
	}
	return false
}

func createCallHierarchyTestModule(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	modContent := "module " + name + "\n\ngo 1.23\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(modContent), 0o644); err != nil {
		t.Fatal(err)
	}
	for fname, content := range files {
		path := filepath.Join(dir, fname)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
