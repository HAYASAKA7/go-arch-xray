package analyzer

import (
	"context"
	"testing"
	"time"
)

func TestRebuildQueue_PrioritizesHighBeforeLow(t *testing.T) {
	q := NewRebuildQueue()
	q.Enqueue(RebuildRequest{priority: PriorityLow, createdAt: time.Unix(1, 0), reason: "low"})
	q.Enqueue(RebuildRequest{priority: PriorityHigh, createdAt: time.Unix(2, 0), reason: "high"})

	req, err := q.Dequeue(context.Background())
	if err != nil {
		t.Fatalf("Dequeue failed: %v", err)
	}
	if req.priority != PriorityHigh {
		t.Fatalf("expected high priority first, got %+v", req)
	}
}

func TestRebuildQueue_StatusTracksPendingAndActive(t *testing.T) {
	q := NewRebuildQueue()
	q.Enqueue(RebuildRequest{priority: PriorityNormal, reason: "watcher"})

	status := q.Status()
	if status.Pending != 1 {
		t.Fatalf("expected one pending request, got %+v", status)
	}

	req, err := q.Dequeue(context.Background())
	if err != nil {
		t.Fatalf("Dequeue failed: %v", err)
	}
	if req.reason != "watcher" {
		t.Fatalf("unexpected dequeued request: %+v", req)
	}
	status = q.Status()
	if status.Active == nil || status.Active.reason != "watcher" {
		t.Fatalf("expected active request to be tracked, got %+v", status)
	}
}

func TestSyncManager_Transitions(t *testing.T) {
	m := NewSyncManager(newTestWorkspace(t), t.TempDir()+"/state.json")
	if status := m.GetStatus(); status.State != StateIdle {
		t.Fatalf("expected idle initial state, got %+v", status)
	}

	if err := m.Trigger("manual rebuild"); err != nil {
		t.Fatalf("Trigger failed: %v", err)
	}
	if status := m.GetStatus(); status.State != StateComputing || status.Reason != "manual rebuild" {
		t.Fatalf("expected computing trigger state, got %+v", status)
	}

	m.Pause()
	if status := m.GetStatus(); status.State != StatePaused {
		t.Fatalf("expected paused state, got %+v", status)
	}
	m.Resume()
	if status := m.GetStatus(); status.State != StateIdle {
		t.Fatalf("expected idle after resume, got %+v", status)
	}
}

func TestSyncManager_RequeuesWhenComputeBusy(t *testing.T) {
	m := NewSyncManager(newTestWorkspace(t), t.TempDir()+"/state.json")
	locked, err := m.mutex.TryLock("other", PriorityHigh)
	if err != nil || !locked {
		t.Fatalf("expected external compute lock, locked=%v err=%v", locked, err)
	}
	defer m.mutex.Unlock("other")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.queue.Enqueue(RebuildRequest{priority: PriorityNormal, reason: "background"})
	go m.backgroundBuildLoop(ctx)

	time.Sleep(100 * time.Millisecond)
	status := m.queue.Status()
	if status.Pending == 0 {
		t.Fatalf("expected work to be requeued while compute is busy, got %+v", status)
	}
}

func TestSyncManager_PersistsStateAfterRebuild(t *testing.T) {
	ws := newTestWorkspace(t)
	root := createTestModule(t, "syncpersist", `package main

func main() {}
`)
	statePath := t.TempDir() + "/state.json"
	m := NewSyncManager(ws, statePath)
	req := RebuildRequest{
		rootPath:  root,
		patterns:  []string{"./..."},
		priority:  PriorityHigh,
		reason:    "manual",
		createdBy: "tool",
	}
	if err := m.runRebuild(context.Background(), req); err != nil {
		t.Fatalf("runRebuild failed: %v", err)
	}

	state, err := LoadState(statePath)
	if err != nil {
		t.Fatalf("LoadState failed: %v", err)
	}
	if state.LastHash == "" || len(state.Files) == 0 {
		t.Fatalf("expected persisted sync state, got %#v", state)
	}
}
