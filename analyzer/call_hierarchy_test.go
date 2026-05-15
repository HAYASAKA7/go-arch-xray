package analyzer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCallGraphTools_BatchedScenarios(t *testing.T) {
	dir := createCallHierarchyTestModule(t, "callbatched", map[string]string{
		"main.go": `package main

func StaticRoot() { StaticA() }
func StaticA() { StaticB() }
func StaticB() { StaticC() }
func StaticC() { StaticD() }
func StaticD() {}

func RecursiveRoot() { RecursiveLoop() }
func RecursiveLoop() { RecursiveLoop() }

func GoRoot() { go GoWorker() }
func GoWorker() {}

func OptionsRoot() { OptionsA(); OptionsX() }
func OptionsA() { OptionsB() }
func OptionsB() {}
func OptionsX() { OptionsY() }
func OptionsY() {}

type orgSyncService struct{}
func (s *orgSyncService) syncUsersWithConflictHandling() {}
func (s *orgSyncService) SyncOrganization() {}
func MethodRoot() {
	svc := &orgSyncService{}
	svc.syncUsersWithConflictHandling()
	svc.SyncOrganization()
}

func PathA() { PathB() }
func PathB() { PathC() }
func PathC() {}
func PathUnreachable() {}

func PathCutoffA() { PathCutoffB1(); PathCutoffB2() }
func PathCutoffB1() { PathCutoffC() }
func PathCutoffB2() { PathCutoffC() }
func PathCutoffC() {}

func CallerRoot() { CallerA() }
func CallerA() { CallerB() }
func CallerB() {}

func CallerOptRoot1() { CallerOptTarget() }
func CallerOptRoot2() { CallerOptTarget() }
func CallerOptRoot3() { CallerOptTarget() }
func CallerOptTarget() {}
`,
	})

	ws := newTestWorkspace(t)

	t.Run("static calls max depth three", func(t *testing.T) {
		result, err := AnalyzeCallHierarchy(ws, dir, "./...", "StaticRoot", 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.RootFunction == "" {
			t.Fatal("expected root function")
		}
		if result.MaxDepth != 3 {
			t.Fatalf("expected max depth 3, got %d", result.MaxDepth)
		}
		if !hasCallEdge(result, "StaticRoot", "StaticA", "Static") {
			t.Fatal("missing StaticRoot -> StaticA static edge")
		}
		if !hasCallEdge(result, "StaticA", "StaticB", "Static") {
			t.Fatal("missing StaticA -> StaticB static edge")
		}
		if !hasCallEdge(result, "StaticB", "StaticC", "Static") {
			t.Fatal("missing StaticB -> StaticC static edge")
		}
		if hasCallEdge(result, "StaticC", "StaticD", "Static") {
			t.Fatal("did not expect StaticC -> StaticD beyond max depth")
		}
	})

	t.Run("deduplicates recursive cycles", func(t *testing.T) {
		result, err := AnalyzeCallHierarchy(ws, dir, "./...", "RecursiveRoot", 3)
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
	})

	t.Run("labels goroutine edges", func(t *testing.T) {
		result, err := AnalyzeCallHierarchy(ws, dir, "./...", "GoRoot", 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !hasCallEdge(result, "GoRoot", "GoWorker", "Goroutine") {
			t.Fatal("missing GoRoot -> GoWorker goroutine edge")
		}
	})

	t.Run("options apply limit offset and summary", func(t *testing.T) {
		result, err := AnalyzeCallHierarchyWithOptions(ws, dir, "./...", "OptionsRoot", 3, QueryOptions{Offset: 1, Limit: 2, Summary: true})
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
	})

	t.Run("accepts receiver qualified method names", func(t *testing.T) {
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
	})

	t.Run("case insensitive fallback", func(t *testing.T) {
		queries := []string{"syncOrganization", "syncorganization", "SYNCORGANIZATION"}
		for _, q := range queries {
			result, err := AnalyzeCallHierarchy(ws, dir, "./...", q, 3)
			if err != nil {
				t.Fatalf("expected case-insensitive query %q to resolve, got error: %v", q, err)
			}
			if !strings.EqualFold(shortFuncName(result.RootFunction), "SyncOrganization") {
				t.Fatalf("query %q: expected root SyncOrganization, got %s", q, result.RootFunction)
			}
		}
	})

	t.Run("find call path reachable direct", func(t *testing.T) {
		result, err := FindCallPath(ws, dir, "./...", "PathA", "PathC", 8, 20)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.Reachable {
			t.Fatal("expected PathC to be reachable from PathA")
		}
		found := false
		for _, p := range result.Paths {
			if len(p.Steps) == 3 {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected path with 3 steps (PathA->PathB->PathC), got paths: %v", result.Paths)
		}
	})

	t.Run("find call path unreachable", func(t *testing.T) {
		result, err := FindCallPath(ws, dir, "./...", "PathA", "PathUnreachable", 8, 20)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Reachable {
			t.Fatal("expected unreachable result")
		}
		if len(result.Paths) != 0 {
			t.Fatalf("expected no paths, got %d", len(result.Paths))
		}
		if result.CutoffReason == "" {
			t.Fatal("expected a cutoff reason when unreachable")
		}
	})

	t.Run("find call path max paths cutoff", func(t *testing.T) {
		result, err := FindCallPath(ws, dir, "./...", "PathCutoffA", "PathCutoffC", 8, 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.Reachable {
			t.Fatal("expected reachable")
		}
		if len(result.Paths) > 1 {
			t.Fatalf("expected at most 1 path due to max_paths, got %d", len(result.Paths))
		}
	})

	t.Run("find call path missing function", func(t *testing.T) {
		_, err := FindCallPath(ws, dir, "./...", "PathA", "NonExistent", 8, 20)
		if err == nil {
			t.Fatal("expected error for missing to_function")
		}
	})

	t.Run("find callers incoming edges", func(t *testing.T) {
		result, err := FindCallers(ws, dir, "./...", "CallerB", 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.RootFunction == "" {
			t.Fatal("expected root function")
		}
		if !hasCallEdge(&CallHierarchyResult{Edges: result.Edges}, "CallerA", "CallerB", "Static") {
			t.Fatal("missing caller edge CallerA -> CallerB")
		}
		if !hasCallEdge(&CallHierarchyResult{Edges: result.Edges}, "CallerRoot", "CallerA", "Static") {
			t.Fatal("missing transitive caller edge CallerRoot -> CallerA")
		}
	})

	t.Run("find callers respects depth", func(t *testing.T) {
		result, err := FindCallers(ws, dir, "./...", "CallerB", 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !hasCallEdge(&CallHierarchyResult{Edges: result.Edges}, "CallerA", "CallerB", "Static") {
			t.Fatal("missing direct caller CallerA -> CallerB")
		}
		if hasCallEdge(&CallHierarchyResult{Edges: result.Edges}, "CallerRoot", "CallerA", "Static") {
			t.Fatal("did not expect CallerRoot -> CallerA beyond max depth")
		}
	})

	t.Run("find callers options apply limit offset", func(t *testing.T) {
		result, err := FindCallersWithOptions(ws, dir, "./...", "CallerOptTarget", 3, QueryOptions{Limit: 1, Offset: 1})
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
		if result.Edges[0].Caller != "callbatched.CallerOptRoot2" {
			t.Fatalf("expected CallerOptRoot2 caller at offset 1, got %s", result.Edges[0].Caller)
		}
	})
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

	ws := newTestWorkspace(t)
	result, err := AnalyzeCallHierarchy(ws, dir, ".", "SyncOrganization", 3)
	if err != nil {
		t.Fatalf("expected dependency method lookup to resolve, got error: %v", err)
	}
	if shortFuncName(result.RootFunction) != "SyncOrganization" {
		t.Fatalf("expected root function SyncOrganization, got %s", result.RootFunction)
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
