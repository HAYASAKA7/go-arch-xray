package analyzer

import (
	"testing"
)

func TestFindCallPath_ReachableDirect(t *testing.T) {
	dir := createCallHierarchyTestModule(t, "callpath_direct", map[string]string{
		"main.go": `package main

func A() { B() }
func B() { C() }
func C() {}
`,
	})

	ws := NewWorkspace()
	result, err := FindCallPath(ws, dir, "./...", "A", "C", 8, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Reachable {
		t.Fatal("expected A to be reachable from C")
	}
	if len(result.Paths) == 0 {
		t.Fatal("expected at least one path")
	}
	// Shortest path must pass through B
	found := false
	for _, p := range result.Paths {
		if len(p.Steps) == 3 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected path with 3 steps (A->B->C), got paths: %v", result.Paths)
	}
}

func TestFindCallPath_Unreachable(t *testing.T) {
	dir := createCallHierarchyTestModule(t, "callpath_unreach", map[string]string{
		"main.go": `package main

func A() { B() }
func B() {}
func C() {}
`,
	})

	ws := NewWorkspace()
	result, err := FindCallPath(ws, dir, "./...", "A", "C", 8, 20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Reachable {
		t.Fatal("expected A to not be reachable from C")
	}
	if len(result.Paths) != 0 {
		t.Fatalf("expected no paths, got %d", len(result.Paths))
	}
	if result.CutoffReason == "" {
		t.Fatal("expected a cutoff reason when unreachable")
	}
}

func TestFindCallPath_MaxPathsCutoff(t *testing.T) {
	// A can reach C via multiple routes
	dir := createCallHierarchyTestModule(t, "callpath_cutoff", map[string]string{
		"main.go": `package main

func A() { B1(); B2() }
func B1() { C() }
func B2() { C() }
func C() {}
`,
	})

	ws := NewWorkspace()
	result, err := FindCallPath(ws, dir, "./...", "A", "C", 8, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Reachable {
		t.Fatal("expected reachable")
	}
	if len(result.Paths) > 1 {
		t.Fatalf("expected at most 1 path due to max_paths, got %d", len(result.Paths))
	}
}

func TestFindCallPath_MissingFunction(t *testing.T) {
	dir := createCallHierarchyTestModule(t, "callpath_miss", map[string]string{
		"main.go": `package main

func A() {}
`,
	})

	ws := NewWorkspace()
	_, err := FindCallPath(ws, dir, "./...", "A", "NonExistent", 8, 20)
	if err == nil {
		t.Fatal("expected error for missing to_function")
	}
}
