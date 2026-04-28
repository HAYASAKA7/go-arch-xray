package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cyanl/go-arch-xray/analyzer"
)

func TestHandlePackageDependencies_ReturnsStructuredDependencies(t *testing.T) {
	dir := createMainTestModule(t, "handlerdeps", map[string]string{
		"app/app.go":  "package app\n\nimport \"handlerdeps/domain\"\n\nfunc Run() string { return domain.Name() }\n",
		"domain/d.go": "package domain\n\nfunc Name() string { return \"domain\" }\n",
	})

	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handlePackageDependencies(context.Background(), nil, PackageDependenciesInput{
		RootPath: dir,
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if toolResult != nil {
		t.Fatalf("expected structured success result, got tool result: %#v", toolResult)
	}
	if result == nil {
		t.Fatal("expected dependency result")
	}
	if !mainHasDependency(result, "handlerdeps/app", "handlerdeps/domain") {
		t.Fatal("missing handlerdeps/app -> handlerdeps/domain dependency")
	}
}

func TestHandleInterfaceTopology_ReturnsToolErrorForInvalidInput(t *testing.T) {
	dir := createMainTestModule(t, "handlerinvalid", map[string]string{
		"main.go": "package main\n\nfunc main() {}\n",
	})

	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handleInterfaceTopology(context.Background(), nil, InterfaceTopologyInput{
		RootPath: dir,
	})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected no structured result, got %#v", result)
	}
	if toolResult == nil || !toolResult.IsError {
		t.Fatalf("expected MCP tool error result, got %#v", toolResult)
	}
}

func TestHandleReloadWorkspace_ReloadsChangedSource(t *testing.T) {
	dir := createMainTestModule(t, "handlerreload", map[string]string{
		"main.go": "package main\n\nfunc Version() string { return \"v1\" }\n",
	})

	workspace = analyzer.NewWorkspace()
	if _, err := workspace.GetOrLoad(dir, "./..."); err != nil {
		t.Fatalf("initial load failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc Version() string { return \"v2\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	toolResult, result, err := handleReloadWorkspace(context.Background(), nil, ReloadWorkspaceInput{RootPath: dir})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if toolResult != nil {
		t.Fatalf("expected structured reload success, got %#v", toolResult)
	}
	if result == nil || result.PackagesLoaded == 0 {
		t.Fatalf("expected reload summary with loaded packages, got %#v", result)
	}
}

func TestHandleAnalyzeCallHierarchy_ReturnsStructuredEdges(t *testing.T) {
	dir := createMainTestModule(t, "handlercalls", map[string]string{
		"main.go": "package main\n\nfunc Root() { Worker() }\nfunc Worker() {}\n",
	})

	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handleAnalyzeCallHierarchy(context.Background(), nil, CallHierarchyInput{
		RootPath:     dir,
		FunctionName: "Root",
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if toolResult != nil {
		t.Fatalf("expected structured call hierarchy success, got %#v", toolResult)
	}
	if result == nil || !mainHasCallEdge(result, "Root", "Worker") {
		t.Fatalf("expected Root -> Worker edge, got %#v", result)
	}
}

func TestHandleAnalyzeCallHierarchy_ReturnsToolErrorForMissingFunction(t *testing.T) {
	dir := createMainTestModule(t, "handlermissingcall", map[string]string{
		"main.go": "package main\n\nfunc Root() {}\n",
	})

	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handleAnalyzeCallHierarchy(context.Background(), nil, CallHierarchyInput{
		RootPath:     dir,
		FunctionName: "Missing",
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected no structured result, got %#v", result)
	}
	if toolResult == nil || !toolResult.IsError {
		t.Fatalf("expected MCP tool error, got %#v", toolResult)
	}
}

func TestHandleTraceStructLifecycle_ReturnsStructuredHops(t *testing.T) {
	dir := createMainTestModule(t, "handlerlife", map[string]string{
		"main.go": "package main\n\ntype User struct{ Name string }\nfunc NewUser() *User { return &User{} }\n",
	})

	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handleTraceStructLifecycle(context.Background(), nil, StructLifecycleInput{
		RootPath:   dir,
		StructName: "User",
		Summary:    true,
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if toolResult != nil {
		t.Fatalf("expected structured lifecycle success, got %#v", toolResult)
	}
	if result == nil || !mainHasLifecycleHop(result, "Instantiate") {
		t.Fatalf("expected Instantiate hop, got %#v", result)
	}
	if result.Summary == nil {
		t.Fatal("expected lifecycle summary")
	}
}

func TestHandleTraceStructLifecycle_ReturnsToolErrorForInvalidInput(t *testing.T) {
	dir := createMainTestModule(t, "handlerlifeinvalid", map[string]string{
		"main.go": "package main\n\nfunc Root() {}\n",
	})

	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handleTraceStructLifecycle(context.Background(), nil, StructLifecycleInput{
		RootPath: dir,
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected no structured result, got %#v", result)
	}
	if toolResult == nil || !toolResult.IsError {
		t.Fatalf("expected MCP tool error, got %#v", toolResult)
	}
}

func TestHandleDetectConcurrencyRisks_ReturnsStructuredRisks(t *testing.T) {
	dir := createMainTestModule(t, "handlerrisk", map[string]string{
		"main.go": "package main\n\ntype State struct{ Count int }\nfunc Run(s *State) { go func() { s.Count++ }() }\n",
	})

	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handleDetectConcurrencyRisks(context.Background(), nil, ConcurrencyRisksInput{
		RootPath: dir,
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if toolResult != nil {
		t.Fatalf("expected structured risk success, got %#v", toolResult)
	}
	if result == nil || !mainHasConcurrencyRisk(result, "High") {
		t.Fatalf("expected high concurrency risk, got %#v", result)
	}
}

func TestHandleFindCallers_ReturnsIncomingEdges(t *testing.T) {
	dir := createMainTestModule(t, "handlercallers", map[string]string{
		"main.go": "package main\n\nfunc Root() { Worker() }\nfunc Worker() {}\n",
	})

	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handleFindCallers(context.Background(), nil, CallersInput{
		RootPath:     dir,
		FunctionName: "Worker",
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if toolResult != nil {
		t.Fatalf("expected structured callers success, got %#v", toolResult)
	}
	if result == nil || len(result.Edges) == 0 {
		t.Fatalf("expected caller edges, got %#v", result)
	}
}

func TestHandleCacheStatusAndClearCache(t *testing.T) {
	dir := createMainTestModule(t, "handlercache", map[string]string{
		"main.go": "package main\n\nfunc Root() {}\n",
	})

	workspace = analyzer.NewWorkspace()
	if _, err := workspace.GetOrLoad(dir, "./..."); err != nil {
		t.Fatalf("initial load failed: %v", err)
	}

	_, status, err := handleCacheStatus(context.Background(), nil, CacheStatusInput{})
	if err != nil {
		t.Fatalf("cache status failed: %v", err)
	}
	if status == nil || status.CacheSize == 0 {
		t.Fatalf("expected non-empty cache status, got %#v", status)
	}

	_, clearRes, err := handleClearCache(context.Background(), nil, ClearCacheInput{All: true})
	if err != nil {
		t.Fatalf("clear cache failed: %v", err)
	}
	if clearRes == nil || clearRes.Cleared == 0 {
		t.Fatalf("expected cleared entries, got %#v", clearRes)
	}
	if clearRes.CacheSize != 0 {
		t.Fatalf("expected empty cache after clear-all, got %#v", clearRes)
	}
}

func mainHasDependency(r *analyzer.DependencyResult, from, to string) bool {
	for _, node := range r.Packages {
		if node.Package != from {
			continue
		}
		for _, imp := range node.Imports {
			if imp == to {
				return true
			}
		}
	}
	return false
}

func mainHasConcurrencyRisk(r *analyzer.ConcurrencyRiskResult, level string) bool {
	for _, risk := range r.Risks {
		if risk.RiskLevel == level {
			return true
		}
	}
	return false
}

func mainHasLifecycleHop(r *analyzer.StructLifecycleResult, kind string) bool {
	for _, hop := range r.Hops {
		if hop.Kind == kind {
			return true
		}
	}
	return false
}

func mainHasCallEdge(r *analyzer.CallHierarchyResult, caller, callee string) bool {
	for _, edge := range r.Edges {
		if analyzerShortName(edge.Caller) == caller && analyzerShortName(edge.Callee) == callee {
			return true
		}
	}
	return false
}

func analyzerShortName(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			return name[i+1:]
		}
	}
	return name
}

func createMainTestModule(t *testing.T, name string, files map[string]string) string {
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
