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
