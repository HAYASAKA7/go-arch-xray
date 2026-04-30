package analyzer

import (
	"strings"
	"testing"
)

func TestTraceStructLifecycle_StreamingChunksAllHops(t *testing.T) {
	dir := createLifecycleTestModule(t, "lifestream", map[string]string{
		"main.go": `package main

type User struct{ Name string }

func Run(u *User) {
	u.Name = "a"
	u.Name = "b"
	u.Name = "c"
	u.Name = "d"
	u.Name = "e"
}
`,
	})

	ws := NewWorkspace()
	first, err := TraceStructLifecycle(ws, dir, "./...", "User", LifecycleOptions{
		DedupeMode: "none",
		MaxHops:    100,
		ChunkSize:  2,
	})
	if err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	if first.ChunkSize != 2 {
		t.Fatalf("expected chunk_size=2, got %d", first.ChunkSize)
	}
	if len(first.Hops) != 2 {
		t.Fatalf("expected 2 hops in first chunk, got %d", len(first.Hops))
	}
	if !first.HasMore || first.NextCursor == "" {
		t.Fatalf("expected has_more+cursor, got has_more=%v cursor=%q", first.HasMore, first.NextCursor)
	}

	collected := append([]LifecycleHop(nil), first.Hops...)
	cursor := first.NextCursor
	guard := 0
	for cursor != "" {
		guard++
		if guard > 10 {
			t.Fatal("streaming did not terminate")
		}
		next, err := TraceStructLifecycle(ws, dir, "./...", "User", LifecycleOptions{
			DedupeMode: "none",
			MaxHops:    100,
			ChunkSize:  2,
			Cursor:     cursor,
		})
		if err != nil {
			t.Fatalf("next chunk: %v", err)
		}
		if len(next.Hops) == 0 {
			t.Fatal("empty mid-stream chunk")
		}
		collected = append(collected, next.Hops...)
		cursor = next.NextCursor
	}

	if len(collected) != first.TotalBeforeTruncate {
		t.Fatalf("collected %d hops, expected %d", len(collected), first.TotalBeforeTruncate)
	}

	seen := make(map[string]bool, len(collected))
	for _, h := range collected {
		key := lifecycleHopKey(h)
		if seen[key] {
			t.Fatalf("duplicate hop across chunks: %s", key)
		}
		seen[key] = true
	}
}

func TestTraceStructLifecycle_StreamingInvalidCursor(t *testing.T) {
	dir := createLifecycleTestModule(t, "lifestreambad", map[string]string{
		"main.go": `package main

type User struct{ Name string }

func Run(u *User) { u.Name = "x" }
`,
	})

	ws := NewWorkspace()
	_, err := TraceStructLifecycle(ws, dir, "./...", "User", LifecycleOptions{
		DedupeMode: "none",
		MaxHops:    10,
		ChunkSize:  1,
		Cursor:     "garbage",
	})
	if err == nil || !strings.Contains(err.Error(), "stream cursor") {
		t.Fatalf("expected stream cursor error, got: %v", err)
	}
}
