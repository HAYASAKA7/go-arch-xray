package analyzer

import (
	"os"
	"path/filepath"
	"strings"
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

func TestAnalyzeCallHierarchyWithOptions_AppliesLimitOffsetAndSummary(t *testing.T) {
	dir := createCallHierarchyTestModule(t, "callopts", map[string]string{
		"main.go": `package main

func Root() { A(); X() }
func A() { B() }
func B() {}
func X() { Y() }
func Y() {}
`,
	})

	ws := NewWorkspace()
	result, err := AnalyzeCallHierarchyWithOptions(ws, dir, "./...", "Root", 3, QueryOptions{Offset: 1, Limit: 2, Summary: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary == nil || result.Summary.TotalEdges == 0 {
		t.Fatal("expected non-empty call hierarchy summary")
	}
	if result.TotalBeforeTruncate <= len(result.Edges) {
		t.Fatalf("expected pagination/truncation to reduce edge count, total=%d window=%d", result.TotalBeforeTruncate, len(result.Edges))
	}
	if !result.Truncated {
		t.Fatal("expected truncated=true when offset/limit applied")
	}
}

func TestAnalyzeCallHierarchy_AcceptsReceiverQualifiedMethodNames(t *testing.T) {
	dir := createCallHierarchyTestModule(t, "callmethodquery", map[string]string{
		"main.go": `package main

type orgSyncService struct{}

func (s *orgSyncService) syncUsersWithConflictHandling() {}

func Root() {
	svc := &orgSyncService{}
	svc.syncUsersWithConflictHandling()
}
`,
	})

	ws := NewWorkspace()
	queries := []string{
		"syncUsersWithConflictHandling",
		"*orgSyncService.syncUsersWithConflictHandling",
		"(*orgSyncService).syncUsersWithConflictHandling",
	}

	for _, q := range queries {
		if _, err := AnalyzeCallHierarchy(ws, dir, "./...", q, 3); err != nil {
			t.Fatalf("expected method query %q to resolve, got error: %v", q, err)
		}
	}
}

func TestAnalyzeCallHierarchy_ResolvesDependencyMethodWithNarrowPattern(t *testing.T) {
	dir := createCallHierarchyTestModule(t, "callmethoddep", map[string]string{
		"main.go": `package main

import "callmethoddep/services"

func Root() {
	svc := &services.OrgSyncService{}
	svc.SyncOrganization()
}
`,
		"services/service.go": `package services

type OrgSyncService struct{}

func (s *OrgSyncService) SyncOrganization() {}
`,
	})

	ws := NewWorkspace()
	result, err := AnalyzeCallHierarchy(ws, dir, ".", "SyncOrganization", 3)
	if err != nil {
		t.Fatalf("expected dependency method lookup to resolve, got error: %v", err)
	}
	if shortFuncName(result.RootFunction) != "SyncOrganization" {
		t.Fatalf("expected root function SyncOrganization, got %s", result.RootFunction)
	}
}

func TestAnalyzeCallHierarchy_CaseInsensitiveFallback(t *testing.T) {
	dir := createCallHierarchyTestModule(t, "callcasefold", map[string]string{
		"main.go": `package main

type orgSyncService struct{}

func (s *orgSyncService) SyncOrganization() {}

func Root() {
	svc := &orgSyncService{}
	svc.SyncOrganization()
}
`,
	})

	ws := NewWorkspace()
	// "syncOrganization" (lowercase s) should case-insensitively match "SyncOrganization"
	queries := []string{
		"syncOrganization",
		"syncorganization",
		"SYNCORGANIZATION",
	}
	for _, q := range queries {
		result, err := AnalyzeCallHierarchy(ws, dir, "./...", q, 3)
		if err != nil {
			t.Fatalf("expected case-insensitive query %q to resolve, got error: %v", q, err)
		}
		if !strings.EqualFold(shortFuncName(result.RootFunction), "SyncOrganization") {
			t.Fatalf("query %q: expected root SyncOrganization, got %s", q, result.RootFunction)
		}
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
