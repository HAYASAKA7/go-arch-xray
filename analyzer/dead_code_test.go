package analyzer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindDeadCode_BatchedScenarios(t *testing.T) {
	dir := createTestModuleFiles(t, "dc_batched", map[string]string{
		"main.go": `package main

import "dc_batched/mcp"

func main() {
	mcp.AddTool(nil, nil, handleTool)
	used()
	go worker()
}

func unusedHelper() int { return 1 }

func used() {}

func ExportedUnused() {}

func deadHere() {}

func deadCaller() { deadTarget() }
func deadTarget() {}

func handleTool() { helperTool() }
func helperTool() {}

func worker() { helper() }
func helper() {}

type Server struct{}
func (s *Server) Start() { _ = 1 }
`,
		"mcp/mcp.go": `package mcp

func AddTool(server any, tool any, handler func()) {}
`,
	})
	ws := newTestWorkspace(t)
	result, err := FindDeadCode(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Run("detects unreferenced function", func(t *testing.T) {
		fn := findDeadFunction(result, ".unusedHelper")
		if fn == nil {
			t.Fatalf("expected unusedHelper in dead code report, got: %+v", result.Functions)
		}
		if fn.Kind != DeadCodeUnreferenced {
			t.Errorf("expected kind=unreferenced, got %s", fn.Kind)
		}
		if fn.Exported {
			t.Error("expected exported=false for lowercase name")
		}
	})

	t.Run("live function not reported", func(t *testing.T) {
		if fn := findDeadFunction(result, ".used"); fn != nil {
			t.Fatalf("live function 'used' wrongly reported as dead: %+v", fn)
		}
	})

	t.Run("callback-registered handler not reported", func(t *testing.T) {
		if fn := findDeadFunction(result, ".handleTool"); fn != nil {
			t.Fatalf("callback-registered handler wrongly reported as dead: %+v", fn)
		}
		if fn := findDeadFunction(result, ".helperTool"); fn != nil {
			t.Fatalf("callback helper wrongly reported as dead: %+v", fn)
		}
	})

	t.Run("exported excluded by default", func(t *testing.T) {
		if fn := findDeadFunction(result, ".ExportedUnused"); fn != nil {
			t.Fatalf("exported function should be excluded by default, got %+v", fn)
		}
		if fn := findDeadFunction(result, ".Start"); fn != nil {
			t.Fatalf("exported pointer-receiver method should be gated out by default, got %+v", fn)
		}
	})

	t.Run("notes present", func(t *testing.T) {
		if len(result.Notes) == 0 {
			t.Fatal("expected non-empty caveats in Notes")
		}
	})

	t.Run("source location present", func(t *testing.T) {
		fn := findDeadFunction(result, ".deadHere")
		if fn == nil {
			t.Fatal("deadHere not in report")
		}
		if fn.File == "" || fn.Line == 0 {
			t.Errorf("expected file/line populated, got file=%q line=%d", fn.File, fn.Line)
		}
		if fn.Anchor == "" {
			t.Error("expected non-empty context anchor")
		}
	})

	t.Run("unreachable from entrypoint", func(t *testing.T) {
		fn := findDeadFunction(result, ".deadTarget")
		if fn != nil {
			t.Fatalf("unreachable helper chain should be hidden in precision mode, got: %+v", fn)
		}
	})

	t.Run("goroutine target is alive", func(t *testing.T) {
		if fn := findDeadFunction(result, ".worker"); fn != nil {
			t.Fatalf("goroutine-reachable worker wrongly reported dead: %+v", fn)
		}
		if fn := findDeadFunction(result, ".helper"); fn != nil {
			t.Fatalf("goroutine-reachable helper wrongly reported dead: %+v", fn)
		}
	})

	audit, err := FindDeadCodeWithOptions(ws, dir, "./...", DeadCodeOptions{IncludeExported: true, Mode: DeadCodeAuditMode}, QueryOptions{})
	if err != nil {
		t.Fatalf("unexpected audit error: %v", err)
	}

	t.Run("audit mode reports unreachable chain", func(t *testing.T) {
		fn := findDeadFunction(audit, ".deadTarget")
		if fn == nil {
			t.Fatalf("expected deadTarget in audit report, got: %+v", audit.Functions)
		}
		if fn.Kind != DeadCodeUnreachable {
			t.Errorf("expected kind=unreachable_from_entrypoint, got %s", fn.Kind)
		}
		if fn.Confidence != "medium" {
			t.Errorf("expected confidence=medium, got %s", fn.Confidence)
		}
		if fn.Actionability != "verify_before_delete" {
			t.Errorf("expected actionability=verify_before_delete, got %s", fn.Actionability)
		}
	})

	t.Run("include exported reports exported function", func(t *testing.T) {
		fn := findDeadFunction(audit, ".ExportedUnused")
		if fn == nil {
			t.Fatalf("expected ExportedUnused when include_exported=true, got: %+v", audit.Functions)
		}
		if !fn.Exported {
			t.Error("expected exported=true")
		}
	})

	t.Run("include exported reports pointer receiver method", func(t *testing.T) {
		fn := findDeadFunction(audit, ".Start")
		if fn == nil {
			t.Fatalf("expected *Server.Start in audit report, got: %+v", audit.Functions)
		}
		if !fn.Exported {
			t.Errorf("expected exported=true for *Server.Start, got false")
		}
	})
}

func TestFindDeadCode_ScopePatternFiltersSiblingPackages(t *testing.T) {
	dir := createTestModuleFiles(t, "dc_scope", map[string]string{
		"sync/sync.go": `package sync

func main() {
	used()
}

func used() {}
func localDead() {}
`,
		"webdav/webdav.go": `package webdav

func helper() {}
func deadNeighbor() {}
`,
	})
	ws := newTestWorkspace(t)
	result, err := FindDeadCodeWithOptions(ws, dir, "./...", DeadCodeOptions{
		Mode:         DeadCodePrecisionMode,
		ScopePattern: "./sync/...",
	}, QueryOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, fn := range result.Functions {
		if strings.Contains(fn.Package, "webdav") {
			t.Fatalf("expected scope filter to exclude sibling package, got %#v", fn)
		}
	}
	if result.ScopePattern != "./sync/..." {
		t.Fatalf("expected scope_pattern to round-trip, got %q", result.ScopePattern)
	}
}

func TestFindDeadCode_StreamingChunkSize(t *testing.T) {
	dir := createTestModule(t, "dc_stream", `package main

func main() {}

func d1() {}
func d2() {}
func d3() {}
func d4() {}
`)
	ws := newTestWorkspace(t)
	result, err := FindDeadCodeWithOptions(ws, dir, "./...", DeadCodeOptions{}, QueryOptions{ChunkSize: 2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Functions) > 2 {
		t.Errorf("expected at most 2 items in chunk, got %d", len(result.Functions))
	}
	if !result.HasMore {
		t.Error("expected has_more=true with 4 dead funcs and chunk_size=2")
	}
	if result.NextCursor == "" {
		t.Error("expected non-empty next_cursor when has_more=true")
	}
}

func findDeadFunction(r *DeadCodeResult, suffix string) *DeadFunction {
	for i := range r.Functions {
		if strings.HasSuffix(r.Functions[i].Function, suffix) {
			return &r.Functions[i]
		}
	}
	return nil
}

func createTestModuleFiles(t *testing.T, name string, files map[string]string) string {
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
