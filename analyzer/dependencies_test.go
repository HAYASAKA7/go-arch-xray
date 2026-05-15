package analyzer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetPackageDependencies_BatchedScenarios(t *testing.T) {
	dir := createDependencyTestModule(t, "depbatch", map[string]string{
		"app/app.go":        "package app\n\nimport \"depbatch/domain\"\n\nfunc Run() string { return domain.Name() }\n",
		"domain/d.go":       "package domain\n\nfunc Name() string { return \"domain\" }\n",
		"unused/u.go":       "package unused\n\nfunc Value() int { return 1 }\n",
		"stdlib/s.go":       "package stdlib\n\nimport \"fmt\"\n\nfunc Message() string { return fmt.Sprint(\"hi\") }\n",
		"a/a.go":            "package a\n\nimport (\n\t\"depbatch/b\"\n\t\"depbatch/c\"\n)\n\nfunc A() { b.B(); c.C() }\n",
		"b/b.go":            "package b\n\nfunc B() {}\n",
		"c/c.go":            "package c\n\nfunc C() {}\n",
		"sub/shared/s.go":   "package shared\n\nfunc Name() string { return \"shared\" }\n",
		"sub/services/s.go": "package services\n\nimport \"depbatch/sub/shared\"\n\nfunc Sync() string { return shared.Name() }\n",
	})

	ws := newTestWorkspace(t)

	t.Run("defaults empty pattern to all packages", func(t *testing.T) {
		result, err := GetPackageDependencies(ws, dir, "", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !hasPackage(result, "depbatch/app") {
			t.Fatal("missing app package")
		}
		if !hasDependency(result, "depbatch/app", "depbatch/domain") {
			t.Fatal("missing app -> domain dependency")
		}
	})

	t.Run("excludes stdlib by default", func(t *testing.T) {
		result, err := GetPackageDependencies(ws, dir, "./...", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if hasDependency(result, "depbatch/stdlib", "fmt") {
			t.Fatal("did not expect stdlib dependency when includeStdlib is false")
		}
	})

	t.Run("includes stdlib when requested", func(t *testing.T) {
		result, err := GetPackageDependencies(ws, dir, "./...", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !hasDependency(result, "depbatch/stdlib", "fmt") {
			t.Fatal("expected stdlib dependency when includeStdlib is true")
		}
	})

	t.Run("deterministic order", func(t *testing.T) {
		result, err := GetPackageDependencies(ws, dir, "./...", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for i := 1; i < len(result.Packages); i++ {
			if result.Packages[i-1].Package > result.Packages[i].Package {
				t.Fatalf("packages are not sorted: %#v", result.Packages)
			}
		}
		for _, pkg := range result.Packages {
			for i := 1; i < len(pkg.Imports); i++ {
				if pkg.Imports[i-1] > pkg.Imports[i] {
					t.Fatalf("imports for %s are not sorted: %#v", pkg.Package, pkg.Imports)
				}
			}
		}
	})

	t.Run("context anchors", func(t *testing.T) {
		result, err := GetPackageDependencies(ws, dir, "./...", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, pkg := range result.Packages {
			if pkg.Package == "depbatch/app" {
				if pkg.Anchor == "" {
					t.Fatal("expected non-empty context anchor")
				}
				return
			}
		}
		t.Fatal("missing depbatch/app package")
	})

	t.Run("limit offset summary", func(t *testing.T) {
		result, err := GetPackageDependenciesWithOptions(ws, dir, "./...", false, QueryOptions{Offset: 1, Limit: 1, Summary: true})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Summary == nil || result.Summary.TotalPackages == 0 {
			t.Fatal("expected non-empty dependency summary")
		}
		if result.TotalBeforeTruncate <= len(result.Packages) {
			t.Fatalf("expected pagination/truncation to reduce package count, total=%d window=%d", result.TotalBeforeTruncate, len(result.Packages))
		}
		if !result.Truncated {
			t.Fatal("expected truncated=true when offset/limit applied")
		}
	})

	t.Run("filesystem-like pattern includes imports", func(t *testing.T) {
		result, err := GetPackageDependencies(ws, dir, "sub/services", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !hasDependency(result, "depbatch/sub/services", "depbatch/sub/shared") {
			t.Fatalf("expected depbatch/sub/services to import depbatch/sub/shared, got %#v", result.Packages)
		}
	})
}

func hasPackage(r *DependencyResult, pkg string) bool {
	for _, node := range r.Packages {
		if node.Package == pkg {
			return true
		}
	}
	return false
}

func hasDependency(r *DependencyResult, from, to string) bool {
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

func createDependencyTestModule(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := files["go.mod"]; !ok {
		files["go.mod"] = "module " + name + "\n\ngo 1.23\n"
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
