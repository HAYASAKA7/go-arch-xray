package analyzer

import (
	"strings"
	"testing"
)

func TestFindDeadCode_DetectsUnreferencedFunction(t *testing.T) {
	dir := createTestModule(t, "dc_unref", `package main

func main() {}

func unusedHelper() int { return 1 }
`)
	ws := NewWorkspace()
	result, err := FindDeadCode(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, fn := range result.Functions {
		if strings.HasSuffix(fn.Function, ".unusedHelper") {
			found = true
			if fn.Kind != DeadCodeUnreferenced {
				t.Errorf("expected kind=unreferenced, got %s", fn.Kind)
			}
			if fn.Exported {
				t.Error("expected exported=false for lowercase name")
			}
		}
	}
	if !found {
		t.Fatalf("expected unusedHelper in dead code report, got: %+v", result.Functions)
	}
}

func TestFindDeadCode_LiveFunctionNotReported(t *testing.T) {
	dir := createTestModule(t, "dc_live", `package main

func main() { used() }

func used() {}
`)
	ws := NewWorkspace()
	result, err := FindDeadCode(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, fn := range result.Functions {
		if strings.HasSuffix(fn.Function, ".used") {
			t.Fatalf("live function 'used' wrongly reported as dead: %+v", fn)
		}
	}
}

func TestFindDeadCode_ExcludesExportedByDefault(t *testing.T) {
	dir := createTestModule(t, "dc_exp", `package main

func main() {}

func ExportedUnused() {}
`)
	ws := NewWorkspace()
	result, err := FindDeadCode(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, fn := range result.Functions {
		if strings.HasSuffix(fn.Function, ".ExportedUnused") {
			t.Fatalf("exported function should be excluded by default, got %+v", fn)
		}
	}
}

func TestFindDeadCode_IncludeExportedReportsExported(t *testing.T) {
	dir := createTestModule(t, "dc_inc_exp", `package main

func main() {}

func ExportedUnused() {}
`)
	ws := NewWorkspace()
	result, err := FindDeadCodeWithOptions(ws, dir, "./...", DeadCodeOptions{IncludeExported: true}, QueryOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, fn := range result.Functions {
		if strings.HasSuffix(fn.Function, ".ExportedUnused") {
			found = true
			if !fn.Exported {
				t.Error("expected exported=true")
			}
		}
	}
	if !found {
		t.Fatalf("expected ExportedUnused when include_exported=true, got: %+v", result.Functions)
	}
}

func TestFindDeadCode_NotesPresent(t *testing.T) {
	dir := createTestModule(t, "dc_notes", `package main

func main() {}
`)
	ws := NewWorkspace()
	result, err := FindDeadCode(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Notes) == 0 {
		t.Fatal("expected non-empty caveats in Notes")
	}
}

func TestFindDeadCode_ReportsSourceLocation(t *testing.T) {
	dir := createTestModule(t, "dc_loc", `package main

func main() {}

func deadHere() {}
`)
	ws := NewWorkspace()
	result, err := FindDeadCode(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, fn := range result.Functions {
		if strings.HasSuffix(fn.Function, ".deadHere") {
			if fn.File == "" || fn.Line == 0 {
				t.Errorf("expected file/line populated, got file=%q line=%d", fn.File, fn.Line)
			}
			if fn.Anchor == "" {
				t.Error("expected non-empty context anchor")
			}
			return
		}
	}
	t.Fatal("deadHere not in report")
}

func TestFindDeadCode_StreamingChunkSize(t *testing.T) {
	dir := createTestModule(t, "dc_stream", `package main

func main() {}

func d1() {}
func d2() {}
func d3() {}
func d4() {}
`)
	ws := NewWorkspace()
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

// Regression: a function that has callers but whose entire caller chain is
// dead must be reported as unreachable_from_entrypoint, not unreferenced.
func TestFindDeadCode_DetectsUnreachableFromEntrypoint(t *testing.T) {
	dir := createTestModule(t, "dc_unreach", `package main

func main() {}

func deadCaller() { deadTarget() }
func deadTarget() {}
`)
	ws := NewWorkspace()
	result, err := FindDeadCode(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var target *DeadFunction
	for i := range result.Functions {
		if strings.HasSuffix(result.Functions[i].Function, ".deadTarget") {
			target = &result.Functions[i]
			break
		}
	}
	if target == nil {
		t.Fatalf("expected deadTarget in report, got: %+v", result.Functions)
	}
	if target.Kind != DeadCodeUnreachable {
		t.Errorf("expected kind=unreachable_from_entrypoint, got %s", target.Kind)
	}
}

// Regression: goroutine spawn sites must seed reachability so a function only
// invoked via `go` is treated as alive.
func TestFindDeadCode_GoroutineTargetIsAlive(t *testing.T) {
	dir := createTestModule(t, "dc_go", `package main

func main() {
	go worker()
}

func worker() {
	helper()
}

func helper() {}
`)
	ws := NewWorkspace()
	result, err := FindDeadCode(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, fn := range result.Functions {
		if strings.HasSuffix(fn.Function, ".worker") || strings.HasSuffix(fn.Function, ".helper") {
			t.Fatalf("goroutine-reachable func wrongly reported dead: %+v", fn)
		}
	}
}

// Regression: pointer-receiver method on an exported struct must classify as
// exported so include_exported gating works correctly.
func TestFindDeadCode_PointerReceiverExportedMethodGated(t *testing.T) {
	dir := createTestModule(t, "dc_ptr_recv", `package main

type Server struct{}

func (s *Server) Start() { _ = 1 }

func main() {}
`)
	ws := NewWorkspace()
	// Default: should NOT appear (exported gated out).
	r1, err := FindDeadCode(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, fn := range r1.Functions {
		if strings.HasSuffix(fn.Function, ".Start") {
			t.Fatalf("exported pointer-receiver method should be gated out by default, got %+v", fn)
		}
	}
	// With include_exported: SHOULD appear and exported=true.
	ws2 := NewWorkspace()
	r2, err := FindDeadCodeWithOptions(ws2, dir, "./...", DeadCodeOptions{IncludeExported: true}, QueryOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, fn := range r2.Functions {
		if strings.HasSuffix(fn.Function, ".Start") {
			found = true
			if !fn.Exported {
				t.Errorf("expected exported=true for *Server.Start, got false")
			}
		}
	}
	if !found {
		t.Fatalf("expected *Server.Start in include_exported report, got: %+v", r2.Functions)
	}
}
