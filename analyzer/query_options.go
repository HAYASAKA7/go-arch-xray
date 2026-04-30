package analyzer

import (
	"os"
	"strconv"
)

// DefaultMaxChunkSize is the maximum number of items returned per streaming
// call regardless of what the caller requests via ChunkSize. AI clients
// frequently pass 100-200 because that reads as "small" relative to whole
// codebases, but per-item payload (handler/anchor/file/line) accumulates
// fast: 100 HTTP routes ≈ 10-12k tokens, which can fill an LLM context
// window in a single response. Capping server-side keeps streamed responses
// LLM-friendly while still letting clients raise the cap explicitly via
// the GO_ARCH_XRAY_MAX_CHUNK_SIZE environment variable when they're sure
// their transport and context budget can handle it.
const DefaultMaxChunkSize = 50

// effectiveMaxChunkSize returns the runtime chunk-size cap, honoring the
// GO_ARCH_XRAY_MAX_CHUNK_SIZE override when set to a positive integer.
// Invalid or non-positive overrides fall back to the default.
func effectiveMaxChunkSize() int {
	if v := os.Getenv("GO_ARCH_XRAY_MAX_CHUNK_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return DefaultMaxChunkSize
}

// clampChunkSize enforces the server-side ChunkSize cap. Values <= 0 pass
// through unchanged so callers can distinguish "no streaming" from
// "streaming with default cap". This is the single source of truth for
// ChunkSize clamping; both streaming infrastructure and per-tool result
// reporting should route through it to stay consistent.
func clampChunkSize(n int) int {
	if n <= 0 {
		return n
	}
	if cap := effectiveMaxChunkSize(); n > cap {
		return cap
	}
	return n
}

// QueryOptions provides consistent high-volume output controls across tools.
// Zero values are backward-compatible (no pagination/truncation).
//
// Streaming: when ChunkSize > 0, results are emitted in fixed-size chunks
// with an opaque continuation Cursor. Streaming overrides Limit and Offset
// and is intended for tools that may produce very large outputs (e.g.
// call hierarchies and lifecycle traces). MaxItems still applies as a
// global safety cap on the addressable dataset before chunking.
//
// ChunkSize is silently capped at effectiveMaxChunkSize() (default 50) to
// keep individual streamed responses inside typical LLM context budgets.
type QueryOptions struct {
	Limit     int
	Offset    int
	MaxItems  int
	Summary   bool
	Cursor    string
	ChunkSize int
	// Export selects an optional diagram representation rendered into the
	// result's Diagram field. Zero value (ExportNone) preserves the
	// historical JSON-only payload, so adding this option is fully
	// backward-compatible for callers that omit it.
	Export ExportFormat
}

func normalizeQueryOptions(opts QueryOptions) QueryOptions {
	if opts.Offset < 0 {
		opts.Offset = 0
	}
	if opts.Limit < 0 {
		opts.Limit = 0
	}
	if opts.MaxItems < 0 {
		opts.MaxItems = 0
	}
	if opts.ChunkSize < 0 {
		opts.ChunkSize = 0
	}
	opts.ChunkSize = clampChunkSize(opts.ChunkSize)
	return opts
}

// applyQueryWindow applies offset/limit/max_items to an in-memory collection.
// It returns the selected window, the original total, and whether output was
// truncated or shifted from the original full list.
func applyQueryWindow[T any](items []T, opts QueryOptions) ([]T, int, bool) {
	opts = normalizeQueryOptions(opts)
	total := len(items)
	if total == 0 {
		return []T{}, 0, false
	}

	start := opts.Offset
	if start > total {
		start = total
	}
	end := total
	if opts.MaxItems > 0 && start+opts.MaxItems < end {
		end = start + opts.MaxItems
	}
	if opts.Limit > 0 && start+opts.Limit < end {
		end = start + opts.Limit
	}
	if end < start {
		end = start
	}

	window := append([]T(nil), items[start:end]...)
	truncated := start > 0 || end < total
	return window, total, truncated
}
