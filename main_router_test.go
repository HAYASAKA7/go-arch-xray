package main

import (
	"context"
	"testing"

	"github.com/HAYASAKA7/go-arch-xray/analyzer"
)

func TestHandleAnalyzeCallHierarchy_ReportsBusyWhenComputeLocked(t *testing.T) {
	dir := createMainTestModule(t, "routerbusy", map[string]string{
		"main.go": "package main\n\nfunc Root() { Worker() }\nfunc Worker() {}\n",
	})

	router := newQueryRouter(workspace, nil)
	locked, err := router.computeMutex.TryLock("background", analyzer.PriorityNormal)
	if err != nil || !locked {
		t.Fatalf("expected background lock, locked=%v err=%v", locked, err)
	}
	defer router.computeMutex.Unlock("background")

	result, out, err := router.handleAnalyzeCallHierarchy(context.Background(), nil, CallHierarchyInput{
		RootPath:     dir,
		FunctionName: "Root",
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if out != nil {
		t.Fatalf("expected no structured output when busy, got %#v", out)
	}
	if result == nil || result.IsError {
		t.Fatalf("expected non-error busy result, got %#v", result)
	}
	if result.Meta == nil || result.Meta["status"] != "busy" {
		t.Fatalf("expected busy meta, got %#v", result.Meta)
	}
}
