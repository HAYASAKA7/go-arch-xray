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

func TestNormalizeQueryOptions_ClampsChunkSize(t *testing.T) {
	t.Setenv("GO_ARCH_XRAY_MAX_CHUNK_SIZE", "")
	in := QueryOptions{ChunkSize: 500}
	out := normalizeQueryOptions(in)
	if out.ChunkSize != DefaultMaxChunkSize {
		t.Fatalf("expected ChunkSize clamped to %d, got %d", DefaultMaxChunkSize, out.ChunkSize)
	}
}

func TestNormalizeQueryOptions_PreservesSmallChunkSize(t *testing.T) {
	t.Setenv("GO_ARCH_XRAY_MAX_CHUNK_SIZE", "")
	out := normalizeQueryOptions(QueryOptions{ChunkSize: 20})
	if out.ChunkSize != 20 {
		t.Fatalf("expected ChunkSize=20 preserved, got %d", out.ChunkSize)
	}
}

func TestNormalizeQueryOptions_NegativeChunkSizeBecomesZero(t *testing.T) {
	out := normalizeQueryOptions(QueryOptions{ChunkSize: -3})
	if out.ChunkSize != 0 {
		t.Fatalf("expected negative ChunkSize normalized to 0, got %d", out.ChunkSize)
	}
}

func TestClampChunkSize_HonorsEnvOverride(t *testing.T) {
	t.Setenv("GO_ARCH_XRAY_MAX_CHUNK_SIZE", "200")
	if got := clampChunkSize(150); got != 150 {
		t.Fatalf("expected 150 to pass through under override=200, got %d", got)
	}
	if got := clampChunkSize(500); got != 200 {
		t.Fatalf("expected 500 clamped to 200 under override, got %d", got)
	}
}

func TestClampChunkSize_InvalidEnvFallsBackToDefault(t *testing.T) {
	t.Setenv("GO_ARCH_XRAY_MAX_CHUNK_SIZE", "not-a-number")
	if got := clampChunkSize(500); got != DefaultMaxChunkSize {
		t.Fatalf("expected fallback to %d on invalid env, got %d", DefaultMaxChunkSize, got)
	}
}

func TestClampChunkSize_ZeroAndNegativePassThrough(t *testing.T) {
	if got := clampChunkSize(0); got != 0 {
		t.Fatalf("expected 0 to pass through, got %d", got)
	}
	if got := clampChunkSize(-5); got != -5 {
		t.Fatalf("expected negative to pass through, got %d", got)
	}
}
