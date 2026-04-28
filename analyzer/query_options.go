package analyzer

// QueryOptions provides consistent high-volume output controls across tools.
// Zero values are backward-compatible (no pagination/truncation).
type QueryOptions struct {
	Limit    int
	Offset   int
	MaxItems int
	Summary  bool
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
	return opts
}

// applyQueryWindow applies offset/limit/max_items to an in-memory collection.
// It returns the selected window, the original total, and whether output was
// truncated or shifted from the original full list.
func applyQueryWindow[T any](items []T, opts QueryOptions) ([]T, int, bool) {
	opts = normalizeQueryOptions(opts)
	total := len(items)
	if total == 0 {
		return nil, 0, false
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
