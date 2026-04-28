package analyzer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetPackageDependencies_DefaultsEmptyPatternToAllPackages(t *testing.T) {
	dir := createDependencyTestModule(t, "depdefault", map[string]string{
		"app/app.go":  "package app\n\nimport \"depdefault/domain\"\n\nfunc Run() string { return domain.Name() }\n",
		"domain/d.go": "package domain\n\nfunc Name() string { return \"domain\" }\n",
		"unused/u.go": "package unused\n\nfunc Value() int { return 1 }\n",
	})

	ws := NewWorkspace()
	result, err := GetPackageDependencies(ws, dir, "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !hasPackage(result, "depdefault/app") {
		t.Fatal("missing app package")
	}
	if !hasDependency(result, "depdefault/app", "depdefault/domain") {
		t.Fatal("missing app -> domain dependency")
	}
}

func TestGetPackageDependencies_ExcludesStdlibByDefault(t *testing.T) {
	dir := createDependencyTestModule(t, "depstdlib", map[string]string{
		"main.go": "package main\n\nimport \"fmt\"\n\nfunc Message() string { return fmt.Sprint(\"hi\") }\n",
	})

	ws := NewWorkspace()
	result, err := GetPackageDependencies(ws, dir, "./...", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if hasDependency(result, "depstdlib", "fmt") {
		t.Fatal("did not expect stdlib dependency when includeStdlib is false")
	}
}

func TestGetPackageDependencies_IncludesStdlibWhenRequested(t *testing.T) {
	dir := createDependencyTestModule(t, "depstdlibon", map[string]string{
		"main.go": "package main\n\nimport \"fmt\"\n\nfunc Message() string { return fmt.Sprint(\"hi\") }\n",
	})

	ws := NewWorkspace()
	result, err := GetPackageDependencies(ws, dir, "./...", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !hasDependency(result, "depstdlibon", "fmt") {
		t.Fatal("expected stdlib dependency when includeStdlib is true")
	}
}

func TestGetPackageDependencies_ReturnsDeterministicPackageAndDependencyOrder(t *testing.T) {
	dir := createDependencyTestModule(t, "depsorted", map[string]string{
		"a/a.go": "package a\n\nimport (\n\t\"depsorted/b\"\n\t\"depsorted/c\"\n)\n\nfunc A() { b.B(); c.C() }\n",
		"b/b.go": "package b\n\nfunc B() {}\n",
		"c/c.go": "package c\n\nfunc C() {}\n",
	})

	ws := NewWorkspace()
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
}

func TestGetPackageDependencies_IncludesContextAnchors(t *testing.T) {
	dir := createDependencyTestModule(t, "depanchors", map[string]string{
		"app/app.go": "package app\n\nfunc Run() {}\n",
	})

	ws := NewWorkspace()
	result, err := GetPackageDependencies(ws, dir, "./...", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, pkg := range result.Packages {
		if pkg.Package == "depanchors/app" {
			if pkg.Anchor == "" {
				t.Fatal("expected non-empty context anchor")
			}
			return
		}
	}
	t.Fatal("missing depanchors/app package")
}

func TestGetPackageDependenciesWithOptions_AppliesLimitOffsetAndSummary(t *testing.T) {
	dir := createDependencyTestModule(t, "depopts", map[string]string{
		"a/a.go": "package a\n\nimport \"depopts/b\"\n\nfunc A() { b.B() }\n",
		"b/b.go": "package b\n\nfunc B() {}\n",
		"c/c.go": "package c\n\nfunc C() {}\n",
	})

	ws := NewWorkspace()
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
