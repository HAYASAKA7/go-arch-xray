package analyzer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSplitPatterns_DefaultsAndDeduplicates(t *testing.T) {
	if got := SplitPatterns(""); len(got) != 1 || got[0] != "./..." {
		t.Fatalf("expected default ./..., got %v", got)
	}
	got := SplitPatterns(" ./internal/...,./pkg/...,./internal/... ")
	if len(got) != 2 || got[0] != "./internal/..." || got[1] != "./pkg/..." {
		t.Fatalf("expected dedup [./internal/..., ./pkg/...], got %v", got)
	}
}

func TestWorkspaceGetOrLoad_NormalizesFilesystemLikePattern(t *testing.T) {
	ws := NewWorkspace()
	dir := createFilesystemPatternModule(t, "pathpattern")

	prog, err := ws.GetOrLoad(dir, "sub/services")
	if err != nil {
		t.Fatalf("load with filesystem-like pattern failed: %v", err)
	}

	if !prog.RootPaths["pathpattern/sub/services"] {
		t.Fatalf("expected normalized root package path, got %v", prog.RootPaths)
	}
	if len(prog.SSAFuncs) == 0 {
		t.Fatal("expected root SSA functions to be loaded")
	}
}

func TestWorkspaceGetOrLoad_MultiplePatternsCacheKeyInvariantToOrder(t *testing.T) {
	ws := NewWorkspace()
	dir := createMultiPatternModule(t, "multipat")

	progA, err := ws.GetOrLoad(dir, "./api/...,./impl/...")
	if err != nil {
		t.Fatalf("first multi-pattern load failed: %v", err)
	}
	progB, err := ws.GetOrLoad(dir, "./impl/...,./api/...")
	if err != nil {
		t.Fatalf("second multi-pattern load failed: %v", err)
	}
	if progA != progB {
		t.Fatal("expected cache key to be order-invariant for multi-pattern loads")
	}
	if len(progA.Patterns) != 2 {
		t.Fatalf("expected 2 patterns recorded, got %v", progA.Patterns)
	}
}

func TestWorkspaceGetOrLoad_LoadsAcrossMultiplePatterns(t *testing.T) {
	ws := NewWorkspace()
	dir := createMultiPatternModule(t, "multipatload")

	prog, err := ws.GetOrLoad(dir, "./api/...,./impl/...")
	if err != nil {
		t.Fatalf("multi-pattern load failed: %v", err)
	}

	wantRoots := map[string]bool{
		"multipatload/api":  true,
		"multipatload/impl": true,
	}
	for path := range wantRoots {
		if !prog.RootPaths[path] {
			t.Fatalf("expected root path %s in multi-pattern load, got %v", path, prog.RootPaths)
		}
	}
}

func TestWorkspaceLRUEvictsLeastRecentlyUsed(t *testing.T) {
	ws := NewWorkspace()
	ws.SetCapacity(2)

	dir1 := createMultiPatternModule(t, "lru1")
	dir2 := createMultiPatternModule(t, "lru2")
	dir3 := createMultiPatternModule(t, "lru3")

	if _, err := ws.GetOrLoad(dir1, "./..."); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.GetOrLoad(dir2, "./..."); err != nil {
		t.Fatal(err)
	}
	if size, _ := ws.Stats(); size != 2 {
		t.Fatalf("expected 2 cached entries, got %d", size)
	}

	// Touch dir1 so it becomes most recent; dir2 should be evicted next.
	if _, err := ws.GetOrLoad(dir1, "./..."); err != nil {
		t.Fatal(err)
	}
	if _, err := ws.GetOrLoad(dir3, "./..."); err != nil {
		t.Fatal(err)
	}
	if size, _ := ws.Stats(); size != 2 {
		t.Fatalf("expected 2 cached entries after eviction, got %d", size)
	}

	// dir2 should have been evicted (LRU); reloading it must produce a fresh program.
	prog2a, err := ws.GetOrLoad(dir2, "./...")
	if err != nil {
		t.Fatal(err)
	}
	prog2b, err := ws.GetOrLoad(dir2, "./...")
	if err != nil {
		t.Fatal(err)
	}
	if prog2a != prog2b {
		t.Fatal("expected reloaded dir2 to be cached after eviction round-trip")
	}
}

func TestLoadedProgramCallGraphCachedAcrossCalls(t *testing.T) {
	ws := NewWorkspace()
	dir := createCallHierarchyTestModule(t, "chacache", map[string]string{
		"main.go": `package main

func Root() { Worker() }
func Worker() {}
`,
	})

	prog, err := ws.GetOrLoad(dir, "./...")
	if err != nil {
		t.Fatal(err)
	}
	g1 := prog.CallGraph()
	g2 := prog.CallGraph()
	if g1 != g2 {
		t.Fatal("expected cached call graph to be reused")
	}
}

func TestLoadedProgramOnlyContainsRootSSAFunctions(t *testing.T) {
	ws := NewWorkspace()
	dir := createCallHierarchyTestModule(t, "rootonly", map[string]string{
		"main.go": `package main

import "fmt"

func Hello() { fmt.Println("hi") }
`,
	})

	prog, err := ws.GetOrLoad(dir, "./...")
	if err != nil {
		t.Fatal(err)
	}

	for _, fn := range prog.SSAFuncs {
		if fn == nil || fn.Pkg == nil || fn.Pkg.Pkg == nil {
			t.Fatalf("found unexpected nil-pkg SSA function in root set: %v", fn)
		}
		if !prog.RootPaths[fn.Pkg.Pkg.Path()] {
			t.Fatalf("expected only root-package SSA functions, got %s in %s", fn.Name(), fn.Pkg.Pkg.Path())
		}
	}
}

func createMultiPatternModule(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(dir, "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "impl"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "extra"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod":       "module " + name + "\n\ngo 1.23\n",
		"api/api.go":   "package api\n\ntype Service interface { Run() error }\n",
		"impl/impl.go": "package impl\n\nimport \"" + name + "/api\"\n\nvar _ api.Service = (*Worker)(nil)\n\ntype Worker struct{}\n\nfunc (Worker) Run() error { return nil }\n",
		"extra/e.go":   "package extra\n\nfunc Unused() {}\n",
	}
	for fname, content := range files {
		path := filepath.Join(dir, fname)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func createFilesystemPatternModule(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(dir, "sub", "services"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub", "shared"), 0o755); err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		"go.mod":                  "module " + name + "\n\ngo 1.23\n",
		"sub/shared/shared.go":    "package shared\n\nfunc Name() string { return \"shared\" }\n",
		"sub/services/service.go": "package services\n\nimport \"" + name + "/sub/shared\"\n\nfunc Sync() string { return shared.Name() }\n",
	}
	for fname, content := range files {
		path := filepath.Join(dir, fname)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return dir
}
