package analyzer

import "testing"

func TestApplyQueryWindow_EmptyReturnsNonNilSlice(t *testing.T) {
	items, total, truncated := applyQueryWindow([]int{}, QueryOptions{})
	if items == nil {
		t.Fatal("expected empty non-nil slice")
	}
	if len(items) != 0 {
		t.Fatalf("expected empty slice, got len=%d", len(items))
	}
	if total != 0 {
		t.Fatalf("expected total=0, got %d", total)
	}
	if truncated {
		t.Fatal("expected truncated=false for empty input")
	}
}
