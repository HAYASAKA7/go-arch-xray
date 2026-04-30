package analyzer

import (
	"strings"
	"testing"
)

// TestStreaming_AllSliceTools exercises cursor-based streaming on every
// slice-returning tool that adopted streamOrWindow, ensuring chunks reassemble
// without duplication and that malformed cursors are rejected.
func TestStreaming_HTTPRoutesChunksAllRoutes(t *testing.T) {
	dir := createDependencyTestModule(t, "routes_stream", map[string]string{
		"main.go": `package main

import "net/http"

func h(w http.ResponseWriter, r *http.Request) {}

func main() {
	http.HandleFunc("/a", h)
	http.HandleFunc("/b", h)
	http.HandleFunc("/c", h)
	http.HandleFunc("/d", h)
	http.HandleFunc("/e", h)
}
`,
	})

	ws := NewWorkspace()
	collected := streamCollect(t, func(cursor string) (any, []HTTPRoute, string, bool) {
		r, err := ListHTTPRoutesWithOptions(ws, dir, "./...", QueryOptions{ChunkSize: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("http_routes stream: %v", err)
		}
		return r, r.Routes, r.NextCursor, r.HasMore
	})
	if len(collected) != 5 {
		t.Fatalf("expected 5 routes across stream, got %d", len(collected))
	}

	if _, err := ListHTTPRoutesWithOptions(ws, dir, "./...", QueryOptions{ChunkSize: 1, Cursor: "garbage"}); err == nil || !strings.Contains(err.Error(), "stream cursor") {
		t.Fatalf("expected stream cursor error, got: %v", err)
	}
}

func TestStreaming_EntrypointsChunksAllItems(t *testing.T) {
	dir := createDependencyTestModule(t, "ep_stream", map[string]string{
		"main.go": `package main

func a() {}
func b() {}
func c() {}

func main() {
	go a()
	go b()
	go c()
}
`,
	})

	ws := NewWorkspace()
	collected := streamCollect(t, func(cursor string) (any, []Entrypoint, string, bool) {
		r, err := ListEntrypointsWithOptions(ws, dir, "./...", QueryOptions{ChunkSize: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("entrypoints stream: %v", err)
		}
		return r, r.Entrypoints, r.NextCursor, r.HasMore
	})
	if len(collected) < 4 { // main + 3 goroutines (init may or may not appear)
		t.Fatalf("expected >=4 entrypoints across stream, got %d", len(collected))
	}
}

func TestStreaming_PackageDependenciesChunks(t *testing.T) {
	dir := createDependencyTestModule(t, "deps_stream", map[string]string{
		"main.go": "package main\n\nfunc main() {}\n",
		"a/a.go":  "package a\n",
		"b/b.go":  "package b\n",
		"c/c.go":  "package c\n",
		"d/d.go":  "package d\n",
	})

	ws := NewWorkspace()
	collected := streamCollect(t, func(cursor string) (any, []PackageDependency, string, bool) {
		r, err := GetPackageDependenciesWithOptions(ws, dir, "./...", false, QueryOptions{ChunkSize: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("deps stream: %v", err)
		}
		return r, r.Packages, r.NextCursor, r.HasMore
	})
	if len(collected) < 4 {
		t.Fatalf("expected at least 4 packages across stream, got %d", len(collected))
	}
}

// streamCollect drives a streaming endpoint to completion and returns the
// concatenated items. fetch returns (whole result for any extra checks,
// chunk items, next cursor, has_more).
func streamCollect[T any](t *testing.T, fetch func(cursor string) (any, []T, string, bool)) []T {
	t.Helper()
	cursor := ""
	guard := 0
	var all []T
	for {
		guard++
		if guard > 50 {
			t.Fatal("stream did not terminate")
		}
		_, chunk, next, hasMore := fetch(cursor)
		if cursor == "" && len(chunk) == 0 && !hasMore {
			return all
		}
		all = append(all, chunk...)
		if !hasMore {
			return all
		}
		if next == "" {
			t.Fatal("has_more=true but next_cursor empty")
		}
		cursor = next
	}
}
