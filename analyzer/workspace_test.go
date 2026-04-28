package analyzer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceGetOrLoad_ReturnsCachedProgram(t *testing.T) {
	ws := NewWorkspace()

	dir := createTestModule(t, "testmod", `package main

func Hello() string { return "hello" }
`)

	prog1, err := ws.GetOrLoad(dir, "./...")
	if err != nil {
		t.Fatalf("first load failed: %v", err)
	}
	if prog1 == nil {
		t.Fatal("expected non-nil LoadedProgram")
	}

	prog2, err := ws.GetOrLoad(dir, "./...")
	if err != nil {
		t.Fatalf("second load failed: %v", err)
	}

	if prog1 != prog2 {
		t.Error("expected same pointer for cached program, got different instances")
	}
}

func TestWorkspaceGetOrLoad_DifferentPatterns(t *testing.T) {
	ws := NewWorkspace()

	dir := createTestModule(t, "testmod2", `package main

func World() string { return "world" }
`)

	prog1, err := ws.GetOrLoad(dir, "./...")
	if err != nil {
		t.Fatalf("load ./... failed: %v", err)
	}

	prog2, err := ws.GetOrLoad(dir, ".")
	if err != nil {
		t.Fatalf("load . failed: %v", err)
	}

	if prog1 == prog2 {
		t.Error("expected different programs for different patterns")
	}
}

func TestWorkspaceGetOrLoad_InvalidPattern(t *testing.T) {
	ws := NewWorkspace()

	_, err := ws.GetOrLoad("/nonexistent/path", "./...")
	if err == nil {
		t.Error("expected error for invalid path, got nil")
	}
}

func TestWorkspaceGetOrLoad_HasSSAProgram(t *testing.T) {
	ws := NewWorkspace()

	dir := createTestModule(t, "testmod3", `package main

func Add(a, b int) int { return a + b }
`)

	prog, err := ws.GetOrLoad(dir, "./...")
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if prog.SSA == nil {
		t.Error("expected non-nil SSA program")
	}
	if len(prog.Packages) == 0 {
		t.Error("expected at least one loaded package")
	}
}

func TestWorkspaceReload_InvalidatesCache(t *testing.T) {
	ws := NewWorkspace()

	dir := createTestModule(t, "testmod4", `package main

func Foo() {}
`)

	prog1, err := ws.GetOrLoad(dir, "./...")
	if err != nil {
		t.Fatalf("first load failed: %v", err)
	}

	ws.Invalidate(dir, "./...")

	prog2, err := ws.GetOrLoad(dir, "./...")
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}

	if prog1 == prog2 {
		t.Error("expected fresh program after invalidation")
	}
}

func TestWorkspaceReload_RefreshesChangedSource(t *testing.T) {
	ws := NewWorkspace()
	dir := createTestModule(t, "reloadsource", `package main

func Version() string { return "v1" }
`)

	prog1, err := ws.GetOrLoad(dir, "./...")
	if err != nil {
		t.Fatalf("initial load failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main

func Version() string { return "v2" }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	prog2, err := ws.Reload(dir, "./...")
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	if prog1 == prog2 {
		t.Fatal("expected reload to replace cached program")
	}
}

func createTestModule(t *testing.T, name, code string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	modContent := "module " + name + "\n\ngo 1.23\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(modContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
