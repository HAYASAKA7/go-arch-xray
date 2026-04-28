package analyzer

import (
	"testing"
)

func TestListEntrypoints_DetectsMainFunction(t *testing.T) {
	dir := createTestModule(t, "ep_main", `package main

func main() {}
`)
	ws := NewWorkspace()
	result, err := ListEntrypoints(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, ep := range result.Entrypoints {
		if ep.Kind == EntrypointMain && ep.Function == "ep_main.main" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected main entrypoint, got: %+v", result.Entrypoints)
	}
}

func TestListEntrypoints_DetectsInitFunction(t *testing.T) {
	dir := createDependencyTestModule(t, "ep_init", map[string]string{
		"pkg/p.go": "package pkg\n\nvar X int\n\nfunc init() { X = 42 }\n",
	})
	ws := NewWorkspace()
	result, err := ListEntrypoints(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, ep := range result.Entrypoints {
		if ep.Kind == EntrypointInit {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected init entrypoint, got: %+v", result.Entrypoints)
	}
}

func TestListEntrypoints_DetectsGoroutineStart(t *testing.T) {
	dir := createTestModule(t, "ep_go", `package main

import "fmt"

func worker() { fmt.Println("working") }

func main() {
	go worker()
}
`)
	ws := NewWorkspace()
	result, err := ListEntrypoints(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, ep := range result.Entrypoints {
		if ep.Kind == EntrypointGoroutine {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected goroutine_start entrypoint, got: %+v", result.Entrypoints)
	}
}

func TestListEntrypoints_MainHasSourceLocation(t *testing.T) {
	dir := createTestModule(t, "ep_loc", `package main

func main() {}
`)
	ws := NewWorkspace()
	result, err := ListEntrypoints(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, ep := range result.Entrypoints {
		if ep.Kind == EntrypointMain {
			if ep.File == "" || ep.Line == 0 {
				t.Errorf("expected main entrypoint to have file/line, got file=%q line=%d", ep.File, ep.Line)
			}
			return
		}
	}
	t.Fatal("main entrypoint not found")
}

func TestListEntrypoints_TotalMatchesSliceLength(t *testing.T) {
	dir := createTestModule(t, "ep_total", `package main

func init() {}
func main() { go func() {}() }
`)
	ws := NewWorkspace()
	result, err := ListEntrypoints(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != len(result.Entrypoints) {
		t.Errorf("Total=%d does not match len(Entrypoints)=%d", result.Total, len(result.Entrypoints))
	}
}
