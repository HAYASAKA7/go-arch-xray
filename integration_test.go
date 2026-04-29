package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cyanl/go-arch-xray/analyzer"
)

func TestE2E_FullAnalysisWorkflow(t *testing.T) {
	// Create a synthetic "real" Go project structure with multiple packages
	// to simulate multi-pattern queries and cache behavior.
	dir := createMainTestModule(t, "e2eproject", map[string]string{
		"cmd/api/main.go":     "package main\n\nimport \"e2eproject/internal/app\"\n\nfunc main() { app.Start() }\n",
		"internal/app/app.go": "package app\n\nimport (\n\t\"e2eproject/internal/db\"\n\t\"e2eproject/pkg/utils\"\n)\n\nfunc Start() {\n\tdb.Connect()\n\tutils.Help()\n}\n",
		"internal/db/db.go":   "package db\n\nfunc Connect() {}\n",
		"pkg/utils/utils.go":  "package utils\n\nfunc Help() {}\n",
	})

	workspace = analyzer.NewWorkspace()

	// 1. Initial Cache Load & Cache Behavior Across Requests
	// Load using a multi-pattern query
	_, reloadRes, err := handleReloadWorkspace(context.Background(), nil, ReloadWorkspaceInput{
		RootPath:        dir,
		PackagePatterns: []string{"./cmd/...", "./internal/...", "./pkg/..."},
	})
	if err != nil {
		t.Fatalf("initial load failed: %v", err)
	}
	if reloadRes.PackagesLoaded != 4 {
		t.Fatalf("expected 4 packages loaded, got %d", reloadRes.PackagesLoaded)
	}

	// 2. Query 1: Find call path from main to utils.Help
	_, callPathRes, err := handleFindCallPath(context.Background(), nil, FindCallPathInput{
		RootPath:        dir,
		FromFunction:    "main",
		ToFunction:      "Help",
		PackagePatterns: []string{"./cmd/...", "./internal/...", "./pkg/..."},
	})
	if err != nil {
		t.Fatalf("find call path failed: %v", err)
	}
	if len(callPathRes.Paths) == 0 {
		t.Fatalf("expected call paths from main to Help")
	}

	// 3. Query 2: Package Dependencies with multi-patterns
	_, depsRes, err := handlePackageDependencies(context.Background(), nil, PackageDependenciesInput{
		RootPath:        dir,
		PackagePatterns: []string{"./cmd/...", "./internal/...", "./pkg/..."},
	})
	if err != nil {
		t.Fatalf("package dependencies failed: %v", err)
	}
	if len(depsRes.Packages) < 4 {
		t.Fatalf("expected at least 4 packages in dependencies graph")
	}

	// 4. Memory usage / Cache Status verification
	_, statusRes, err := handleCacheStatus(context.Background(), nil, CacheStatusInput{})
	if err != nil {
		t.Fatalf("cache status failed: %v", err)
	}
	if statusRes.CacheSize != 1 {
		t.Fatalf("expected 1 cached program, got %d", statusRes.CacheSize)
	}

	// 5. Cache Eviction and Reload Scenario
	// Modify a file to invalidate the cache
	err = os.WriteFile(filepath.Join(dir, "pkg/utils/utils.go"), []byte("package utils\n\nfunc Help() {}\nfunc Extra() {}\n"), 0o644)
	if err != nil {
		t.Fatalf("failed to modify file: %v", err)
	}

	// Reload workspace
	_, reloadRes2, err := handleReloadWorkspace(context.Background(), nil, ReloadWorkspaceInput{
		RootPath:        dir,
		PackagePatterns: []string{"./cmd/...", "./internal/...", "./pkg/..."},
	})
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	if reloadRes2.PackagesLoaded != 4 {
		t.Fatalf("expected 4 packages loaded after reload, got %d", reloadRes2.PackagesLoaded)
	}

	// Verify cache size is still 1 (evicted and replaced)
	_, statusRes2, err := handleCacheStatus(context.Background(), nil, CacheStatusInput{})
	if err != nil {
		t.Fatalf("cache status 2 failed: %v", err)
	}
	if statusRes2.CacheSize != 1 {
		t.Fatalf("expected cache size to remain 1 after reload, got %d", statusRes2.CacheSize)
	}
}
