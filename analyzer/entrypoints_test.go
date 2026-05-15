package analyzer

import "testing"

func TestListEntrypoints_BatchedScenarios(t *testing.T) {
	dir := createDependencyTestModule(t, "ep_batched", map[string]string{
		"main.go": `package main

func init() {}
func worker() {}
func main() {
	go worker()
	go func() {}()
}
`,
	})
	ws := newTestWorkspace(t)
	result, err := ListEntrypoints(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	t.Run("detects main function", func(t *testing.T) {
		found := false
		for _, ep := range result.Entrypoints {
			if ep.Kind == EntrypointMain && ep.Function == "ep_batched.main" {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected main entrypoint, got: %+v", result.Entrypoints)
		}
	})

	t.Run("detects init function", func(t *testing.T) {
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
	})

	t.Run("detects goroutine start", func(t *testing.T) {
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
	})

	t.Run("main has source location", func(t *testing.T) {
		for _, ep := range result.Entrypoints {
			if ep.Kind == EntrypointMain {
				if ep.File == "" || ep.Line == 0 {
					t.Errorf("expected main entrypoint to have file/line, got file=%q line=%d", ep.File, ep.Line)
				}
				return
			}
		}
		t.Fatal("main entrypoint not found")
	})

	t.Run("total matches slice length", func(t *testing.T) {
		if result.Total != len(result.Entrypoints) {
			t.Errorf("Total=%d does not match len(Entrypoints)=%d", result.Total, len(result.Entrypoints))
		}
	})

	t.Run("options apply limit offset", func(t *testing.T) {
		paged, err := ListEntrypointsWithOptions(ws, dir, "./...", QueryOptions{Limit: 1, Offset: 1})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if paged.TotalBeforeTruncate != result.Total {
			t.Fatalf("expected %d total entrypoints before truncate, got %d", result.Total, paged.TotalBeforeTruncate)
		}
		if len(paged.Entrypoints) != 1 {
			t.Fatalf("expected 1 entrypoint due to limit, got %d", len(paged.Entrypoints))
		}
		if !paged.Truncated {
			t.Fatal("expected truncated to be true")
		}
	})
}
