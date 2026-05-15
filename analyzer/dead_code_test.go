package analyzer

import (
	"strings"
	"testing"
)

func TestFindDeadCode_BatchedScenarios(t *testing.T) {
	dir := createTestModule(t, "dc_batched", `package main

func main() {
	used()
	go worker()
}

func unusedHelper() int { return 1 }

func used() {}

func ExportedUnused() {}

func deadHere() {}

func deadCaller() { deadTarget() }
func deadTarget() {}

func worker() { helper() }
func helper() {}

type Server struct{}
func (s *Server) Start() { _ = 1 }
`)
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
		if fn == nil {
			t.Fatalf("expected deadTarget in report, got: %+v", result.Functions)
		}
		if fn.Kind != DeadCodeUnreachable {
			t.Errorf("expected kind=unreachable_from_entrypoint, got %s", fn.Kind)
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

	r2, err := FindDeadCodeWithOptions(ws, dir, "./...", DeadCodeOptions{IncludeExported: true}, QueryOptions{})
	if err != nil {
		t.Fatalf("unexpected include_exported error: %v", err)
	}

	t.Run("include exported reports exported function", func(t *testing.T) {
		fn := findDeadFunction(r2, ".ExportedUnused")
		if fn == nil {
			t.Fatalf("expected ExportedUnused when include_exported=true, got: %+v", r2.Functions)
		}
		if !fn.Exported {
			t.Error("expected exported=true")
		}
	})

	t.Run("include exported reports pointer receiver method", func(t *testing.T) {
		fn := findDeadFunction(r2, ".Start")
		if fn == nil {
			t.Fatalf("expected *Server.Start in include_exported report, got: %+v", r2.Functions)
		}
		if !fn.Exported {
			t.Errorf("expected exported=true for *Server.Start, got false")
		}
	})
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
