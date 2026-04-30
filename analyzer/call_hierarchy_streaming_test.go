package analyzer

import (
	"strings"
	"testing"
)

func TestAnalyzeCallHierarchy_StreamingChunksAllEdges(t *testing.T) {
	dir := createCallHierarchyTestModule(t, "callstream", map[string]string{
		"main.go": `package main

func Root() {
	A()
	B()
	C()
	D()
	E()
}
func A() {}
func B() {}
func C() {}
func D() {}
func E() {}
`,
	})

	ws := NewWorkspace()
	first, err := AnalyzeCallHierarchyWithOptions(ws, dir, "./...", "Root", 3, QueryOptions{ChunkSize: 2})
	if err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	if first.ChunkSize != 2 {
		t.Fatalf("expected chunk_size=2 echoed, got %d", first.ChunkSize)
	}
	if len(first.Edges) != 2 {
		t.Fatalf("expected 2 edges in first chunk, got %d", len(first.Edges))
	}
	if !first.HasMore || first.NextCursor == "" {
		t.Fatalf("expected has_more + next_cursor on first chunk, got has_more=%v cursor=%q", first.HasMore, first.NextCursor)
	}
	if first.TotalBeforeTruncate < 5 {
		t.Fatalf("expected total >= 5, got %d", first.TotalBeforeTruncate)
	}

	collected := append([]CallEdge(nil), first.Edges...)
	cursor := first.NextCursor
	guard := 0
	for cursor != "" {
		guard++
		if guard > 10 {
			t.Fatal("streaming did not terminate")
		}
		next, err := AnalyzeCallHierarchyWithOptions(ws, dir, "./...", "Root", 3, QueryOptions{ChunkSize: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("next chunk: %v", err)
		}
		if len(next.Edges) == 0 {
			t.Fatal("empty chunk before stream completion")
		}
		collected = append(collected, next.Edges...)
		cursor = next.NextCursor
		if cursor == "" && next.HasMore {
			t.Fatal("has_more=true but next_cursor empty")
		}
	}

	if len(collected) != first.TotalBeforeTruncate {
		t.Fatalf("collected %d edges across stream, expected %d", len(collected), first.TotalBeforeTruncate)
	}

	// Ensure no duplicate edges across chunks.
	seen := make(map[string]bool, len(collected))
	for _, e := range collected {
		key := callEdgeKey(e)
		if seen[key] {
			t.Fatalf("duplicate edge across chunks: %s", key)
		}
		seen[key] = true
	}
}

func TestAnalyzeCallHierarchy_StreamingInvalidCursorRejected(t *testing.T) {
	dir := createCallHierarchyTestModule(t, "callstreambad", map[string]string{
		"main.go": `package main

func Root() { A(); B() }
func A() {}
func B() {}
`,
	})

	ws := NewWorkspace()
	_, err := AnalyzeCallHierarchyWithOptions(ws, dir, "./...", "Root", 3, QueryOptions{ChunkSize: 1, Cursor: "not-a-real-cursor"})
	if err == nil {
		t.Fatal("expected error for malformed cursor")
	}
	if !strings.Contains(err.Error(), "stream cursor") {
		t.Fatalf("expected stream cursor error, got: %v", err)
	}
}

func TestAnalyzeCallHierarchy_StreamingFingerprintInvalidation(t *testing.T) {
	dir := createCallHierarchyTestModule(t, "callstreamfp", map[string]string{
		"main.go": `package main

func Root() { A(); B(); C() }
func A() {}
func B() {}
func C() {}
`,
	})

	ws := NewWorkspace()
	first, err := AnalyzeCallHierarchyWithOptions(ws, dir, "./...", "Root", 3, QueryOptions{ChunkSize: 1})
	if err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	if first.NextCursor == "" {
		t.Fatal("expected next_cursor on first chunk")
	}

	// Forge a cursor with a wrong fingerprint to simulate a dataset change.
	bad := encodeStreamCursor(streamCursor{Offset: 1, Total: first.TotalBeforeTruncate, Fingerprint: "deadbeefdeadbeef"})
	_, err = AnalyzeCallHierarchyWithOptions(ws, dir, "./...", "Root", 3, QueryOptions{ChunkSize: 1, Cursor: bad})
	if err == nil || !strings.Contains(err.Error(), "invalidated") {
		t.Fatalf("expected invalidated error, got: %v", err)
	}
}
