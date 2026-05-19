package analyzer

import (
	"context"
	"testing"
)

func TestComputeSnapshotEngine_WritesShadowDatasets(t *testing.T) {
	ws := newTestWorkspace(t)
	dir := createDependencyTestModule(t, "ssabuilder", map[string]string{
		"app/app.go": `package app

import "ssabuilder/domain"

func Run() string {
	return domain.Name()
}
`,
		"domain/domain.go": `package domain

func Name() string { return "domain" }
`,
	})

	store, err := OpenWorkspaceStore(dir)
	if err != nil {
		t.Fatalf("OpenWorkspaceStore failed: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	builder := NewSSABuilder(ws, store, dir, []string{"./..."})
	if err := builder.ComputeAndSnapshot(context.Background()); err != nil {
		t.Fatalf("ComputeAndSnapshot failed: %v", err)
	}

	edges, err := store.GetEdges("ssabuilder/app", "imports")
	if err != nil {
		t.Fatalf("GetEdges failed: %v", err)
	}
	if len(edges) == 0 {
		t.Fatal("expected shadow edges from compute snapshot")
	}
	metrics, err := store.GetMetrics(ComplexityOptions{})
	if err != nil {
		t.Fatalf("GetMetrics failed: %v", err)
	}
	if len(metrics) == 0 {
		t.Fatal("expected shadow metrics from compute snapshot")
	}
}

func TestComputeSnapshotEngine_RespectsComputeMutex(t *testing.T) {
	ws := newTestWorkspace(t)
	dir := createTestModule(t, "ssabuilderbusy", `package main

func main() {}
`)

	store, err := OpenWorkspaceStore(dir)
	if err != nil {
		t.Fatalf("OpenWorkspaceStore failed: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	mutex := NewComputeMutex()
	locked, err := mutex.TryLock("other", PriorityHigh)
	if err != nil || !locked {
		t.Fatalf("expected lock to be held, locked=%v err=%v", locked, err)
	}
	t.Cleanup(func() {
		mutex.Unlock("other")
	})

	builder := NewSSABuilder(ws, store, dir, []string{"./..."})
	builder.mutex = mutex

	if err := builder.ComputeAndSnapshot(context.Background()); err == nil {
		t.Fatal("expected compute in progress error")
	}
}
