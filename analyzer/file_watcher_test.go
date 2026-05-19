package analyzer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileWatcher_DebouncesTriggeredChanges(t *testing.T) {
	q := NewRebuildQueue()
	w := &FileWatcher{
		root:     t.TempDir(),
		debounce: 10 * time.Millisecond,
		changes:  make(chan string, 8),
		done:     make(chan struct{}),
		queue:    q,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.debouncedProcess(ctx)

	w.Trigger("a.go")
	w.Trigger("b.go")
	time.Sleep(30 * time.Millisecond)
	w.Stop()

	status := q.Status()
	if status.Pending != 1 {
		t.Fatalf("expected one rebuild request after debounce, got %+v", status)
	}
}

func TestFileWatcher_ScanAndEnqueue(t *testing.T) {
	store := openTestWorkspaceStore(t)
	file := filepath.Join(store.Layout.Root, "main.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := store.StoreFileMeta(FileMeta{Path: "main.go", Hash: "old"}); err != nil {
		t.Fatalf("StoreFileMeta failed: %v", err)
	}
	q := NewRebuildQueue()
	w := NewFileWatcher(store.Layout.Root, store, q)
	w.debounce = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.debouncedProcess(ctx)
	if err := w.ScanAndEnqueue(context.Background()); err != nil {
		t.Fatalf("ScanAndEnqueue failed: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	if status := q.Status(); status.Pending != 1 {
		t.Fatalf("expected enqueue for changed root scan, got %+v", status)
	}
}
