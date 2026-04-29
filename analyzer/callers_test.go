package analyzer

import "testing"

func TestFindCallers_FindsIncomingEdges(t *testing.T) {
	dir := createCallHierarchyTestModule(t, "callersincoming", map[string]string{
		"main.go": `package main

func Root() { A() }
func A() { B() }
func B() {}
`,
	})

	ws := NewWorkspace()
	result, err := FindCallers(ws, dir, "./...", "B", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RootFunction == "" {
		t.Fatal("expected root function")
	}
	if !hasCallEdge(&CallHierarchyResult{Edges: result.Edges}, "A", "B", "Static") {
		t.Fatal("missing caller edge A -> B")
	}
	if !hasCallEdge(&CallHierarchyResult{Edges: result.Edges}, "Root", "A", "Static") {
		t.Fatal("missing transitive caller edge Root -> A")
	}
}

func TestFindCallers_RespectsDepth(t *testing.T) {
	dir := createCallHierarchyTestModule(t, "callersdepth", map[string]string{
		"main.go": `package main

func Root() { A() }
func A() { B() }
func B() {}
`,
	})

	ws := NewWorkspace()
	result, err := FindCallers(ws, dir, "./...", "B", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasCallEdge(&CallHierarchyResult{Edges: result.Edges}, "A", "B", "Static") {
		t.Fatal("missing direct caller A -> B")
	}
	if hasCallEdge(&CallHierarchyResult{Edges: result.Edges}, "Root", "A", "Static") {
		t.Fatal("did not expect Root -> A beyond max depth")
	}
}

func TestFindCallersWithOptions_AppliesLimitOffsetAndSummary(t *testing.T) {
	dir := createCallHierarchyTestModule(t, "callersopts", map[string]string{
		"main.go": `package main

func Root1() { Target() }
func Root2() { Target() }
func Root3() { Target() }
func Target() {}
`,
	})

	ws := NewWorkspace()
	result, err := FindCallersWithOptions(ws, dir, "./...", "Target", 3, QueryOptions{
		Limit:  1,
		Offset: 1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TotalBeforeTruncate != 3 {
		t.Fatalf("expected 3 total edges before truncate, got %d", result.TotalBeforeTruncate)
	}
	if len(result.Edges) != 1 {
		t.Fatalf("expected 1 edge due to limit, got %d", len(result.Edges))
	}
	if !result.Truncated {
		t.Fatal("expected truncated to be true")
	}
	if result.Edges[0].Caller != "callersopts.Root2" {
		t.Fatalf("expected Root2 caller at offset 1, got %s", result.Edges[0].Caller)
	}
}
