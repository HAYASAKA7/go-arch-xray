package analyzer

import (
	"strings"
	"testing"
)

func TestFindDuplicateMethods_DetectsCopyPaste(t *testing.T) {
	dir := createDependencyTestModule(t, "dup_basic", map[string]string{
		"main.go": "package main\n\nfunc main() { _ = compute(); _ = reckon() }\n",
		"a.go": `package main

func compute() int {
	x := 1
	y := 2
	z := x + y
	return z
}
`,
		"b.go": `package main

func reckon() int {
	x := 1
	y := 2
	z := x + y
	return z
}
`,
	})
	ws := NewWorkspace()
	result, err := FindDuplicateMethods(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total < 1 {
		t.Fatalf("expected at least one duplicate group, got %+v", result)
	}
	found := false
	for _, g := range result.Groups {
		if g.Count != 2 {
			continue
		}
		names := []string{}
		for _, l := range g.Locations {
			names = append(names, l.Name)
		}
		if contains(names, "compute") && contains(names, "reckon") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected compute/reckon group, got: %+v", result.Groups)
	}
}

func TestFindDuplicateMethods_FiltersByMinBodyLines(t *testing.T) {
	dir := createDependencyTestModule(t, "dup_short", map[string]string{
		"main.go": "package main\n\nfunc main() { _ = a(); _ = b() }\n",
		"a.go":    "package main\n\nfunc a() int { return 1 }\n",
		"b.go":    "package main\n\nfunc b() int { return 1 }\n",
	})
	ws := NewWorkspace()
	result, err := FindDuplicateMethodsWithOptions(ws, dir, "./...", DuplicateMethodsOptions{}, QueryOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, g := range result.Groups {
		for _, l := range g.Locations {
			if l.Name == "a" || l.Name == "b" {
				t.Fatalf("short bodies should be filtered by default min_body_lines=3, got group %+v", g)
			}
		}
	}
}

func TestFindDuplicateMethods_RespectsCustomMinBodyLines(t *testing.T) {
	dir := createDependencyTestModule(t, "dup_minlines", map[string]string{
		"main.go": "package main\n\nfunc main() { _ = a(); _ = b() }\n",
		"a.go":    "package main\n\nfunc a() int { return 1 }\n",
		"b.go":    "package main\n\nfunc b() int { return 1 }\n",
	})
	ws := NewWorkspace()
	result, err := FindDuplicateMethodsWithOptions(ws, dir, "./...", DuplicateMethodsOptions{MinBodyLines: 1}, QueryOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.MinBodyLines != 1 {
		t.Errorf("expected MinBodyLines=1 echoed, got %d", result.MinBodyLines)
	}
	if result.Total < 1 {
		t.Fatalf("expected at least one group with min_body_lines=1, got %d", result.Total)
	}
}

func TestFindDuplicateMethods_DistinctSignaturesNotGrouped(t *testing.T) {
	dir := createDependencyTestModule(t, "dup_sig", map[string]string{
		"main.go": "package main\n\nfunc main() { _ = a(); _ = b() }\n",
		"a.go": `package main

func a() int {
	x := 1
	y := 2
	return x + y
}
`,
		"b.go": `package main

func b() string {
	x := 1
	y := 2
	_ = x
	_ = y
	return ""
}
`,
	})
	ws := NewWorkspace()
	result, _ := FindDuplicateMethods(ws, dir, "./...")
	for _, g := range result.Groups {
		names := []string{}
		for _, l := range g.Locations {
			names = append(names, l.Name)
		}
		if contains(names, "a") && contains(names, "b") {
			t.Fatalf("functions with different signatures should not be grouped, got %+v", g)
		}
	}
}

func TestFindDuplicateMethods_NotesPresent(t *testing.T) {
	dir := createTestModule(t, "dup_notes", `package main

func main() {}
`)
	ws := NewWorkspace()
	result, err := FindDuplicateMethods(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Notes) == 0 {
		t.Fatal("expected non-empty notes")
	}
}

func TestFindDuplicateMethods_LocationsHaveAnchors(t *testing.T) {
	dir := createDependencyTestModule(t, "dup_anchors", map[string]string{
		"main.go": "package main\n\nfunc main() { _ = compute(); _ = reckon() }\n",
		"a.go": `package main

func compute() int {
	x := 1
	y := 2
	z := x + y
	return z
}
`,
		"b.go": `package main

func reckon() int {
	x := 1
	y := 2
	z := x + y
	return z
}
`,
	})
	ws := NewWorkspace()
	result, _ := FindDuplicateMethods(ws, dir, "./...")
	for _, g := range result.Groups {
		for _, l := range g.Locations {
			if l.File == "" || l.Line == 0 {
				t.Errorf("expected file/line populated, got %+v", l)
			}
			if !strings.Contains(l.Anchor, "Context Anchor") {
				t.Errorf("expected anchor populated, got %q", l.Anchor)
			}
		}
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// Regression: pointer-receiver methods with identical bodies must group; the
// receiver name extraction handles `*T` correctly.
func TestFindDuplicateMethods_PointerReceiverMethods(t *testing.T) {
	dir := createDependencyTestModule(t, "dup_ptr", map[string]string{
		"main.go": "package main\n\nfunc main() { (&A{}).Run(); (&B{}).Run() }\n",
		"a.go": `package main

type A struct{}

func (a *A) Run() int {
	x := 1
	y := 2
	return x + y
}
`,
		"b.go": `package main

type B struct{}

func (b *B) Run() int {
	x := 1
	y := 2
	return x + y
}
`,
	})
	ws := NewWorkspace()
	result, err := FindDuplicateMethods(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, g := range result.Groups {
		recvs := []string{}
		for _, l := range g.Locations {
			recvs = append(recvs, l.Receiver)
		}
		if contains(recvs, "A") && contains(recvs, "B") {
			found = true
			for _, l := range g.Locations {
				if l.Receiver == "" {
					t.Errorf("pointer receiver not extracted: %+v", l)
				}
			}
		}
	}
	if !found {
		t.Fatalf("expected A.Run / B.Run group, got: %+v", result.Groups)
	}
}

// Regression: streaming chunk_size must paginate the duplicate-groups slice
// and emit a continuation cursor (exercises duplicateGroupKey + streamOrWindow).
func TestFindDuplicateMethods_StreamingChunkSize(t *testing.T) {
	dir := createDependencyTestModule(t, "dup_stream", map[string]string{
		"main.go": "package main\n\nfunc main() { _ = a1(); _ = a2(); _ = b1(); _ = b2(); _ = c1(); _ = c2() }\n",
		"a.go": `package main

func a1() int {
	x := 10
	y := 20
	return x + y
}

func a2() int {
	x := 10
	y := 20
	return x + y
}
`,
		"b.go": `package main

func b1() int {
	p := 1
	q := 2
	r := 3
	return p + q + r
}

func b2() int {
	p := 1
	q := 2
	r := 3
	return p + q + r
}
`,
		"c.go": `package main

func c1() string {
	s := "hello"
	t := "world"
	return s + " " + t
}

func c2() string {
	s := "hello"
	t := "world"
	return s + " " + t
}
`,
	})
	ws := NewWorkspace()
	result, err := FindDuplicateMethodsWithOptions(ws, dir, "./...", DuplicateMethodsOptions{}, QueryOptions{ChunkSize: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Groups) > 1 {
		t.Errorf("expected at most 1 group per chunk, got %d", len(result.Groups))
	}
	if !result.HasMore {
		t.Errorf("expected has_more=true with 3 duplicate groups and chunk_size=1, got total_before_truncate=%d", result.TotalBeforeTruncate)
	}
	if result.NextCursor == "" {
		t.Error("expected non-empty next_cursor when has_more=true")
	}
	if result.ChunkSize != 1 {
		t.Errorf("expected ChunkSize=1 echoed, got %d", result.ChunkSize)
	}
}

// Regression: free function with same body but no receiver should still group
// (covers receiverName empty path).
func TestFindDuplicateMethods_FreeFunctionsHaveEmptyReceiver(t *testing.T) {
	dir := createDependencyTestModule(t, "dup_free", map[string]string{
		"main.go": "package main\n\nfunc main() { _ = compute(); _ = reckon() }\n",
		"a.go": `package main

func compute() int {
	x := 1
	y := 2
	z := x + y
	return z
}
`,
		"b.go": `package main

func reckon() int {
	x := 1
	y := 2
	z := x + y
	return z
}
`,
	})
	ws := NewWorkspace()
	result, _ := FindDuplicateMethods(ws, dir, "./...")
	checked := false
	for _, g := range result.Groups {
		for _, l := range g.Locations {
			if l.Name == "compute" || l.Name == "reckon" {
				checked = true
				if l.Receiver != "" {
					t.Errorf("expected empty receiver for free function, got %q", l.Receiver)
				}
			}
		}
	}
	if !checked {
		t.Fatal("did not encounter free-function locations to verify")
	}
}
