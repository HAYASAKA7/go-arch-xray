package analyzer

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
)

// StreamOptions controls cursor-based streaming for high-volume tool outputs.
//
// Streaming is engaged when ChunkSize > 0. Each call returns at most
// ChunkSize items together with an opaque NextCursor token. Pass that token
// back as Cursor on the following call to resume. The cursor is bound to a
// fingerprint of the underlying dataset so mid-stream changes (e.g. a
// workspace reload) are detected and surfaced as an error rather than
// silently producing inconsistent output.
//
// Streaming is layered on top of existing pagination/safety knobs:
//   - MaxItems still caps the total addressable dataset before chunking.
//   - When ChunkSize > 0, Limit and Offset from QueryOptions are ignored;
//     the cursor controls the offset and ChunkSize controls the limit.
//
// When ChunkSize is 0 (default), behavior is fully backward compatible
// and no streaming metadata is emitted.
type StreamOptions struct {
	Cursor    string
	ChunkSize int
}

const streamCursorVersion = 1

type streamCursor struct {
	Version     int    `json:"v"`
	Offset      int    `json:"o"`
	Total       int    `json:"t"`
	Fingerprint string `json:"f"`
}

func encodeStreamCursor(c streamCursor) string {
	c.Version = streamCursorVersion
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeStreamCursor(s string) (streamCursor, error) {
	var c streamCursor
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return c, fmt.Errorf("invalid stream cursor encoding: %w", err)
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("invalid stream cursor payload: %w", err)
	}
	if c.Version != streamCursorVersion {
		return c, fmt.Errorf("unsupported stream cursor version %d", c.Version)
	}
	return c, nil
}

// streamFingerprint returns a short, deterministic fingerprint identifying a
// dataset snapshot. It mixes the dataset identifier, total size, and the
// first and last item keys, which is sufficient to detect typical mid-stream
// changes (reloads, edits, sort drift) without paying an O(n) hash cost.
func streamFingerprint(identifier string, total int, firstKey, lastKey string) string {
	h := sha256.Sum256([]byte(identifier + "|" + strconv.Itoa(total) + "|" + firstKey + "|" + lastKey))
	return hex.EncodeToString(h[:8])
}

// applyStreamWindow returns the next chunk of items along with continuation
// metadata. items must already be in stable, sorted order. firstKey/lastKey
// are the stable identity keys of items[0] and items[len-1] (or empty
// strings when items is empty); they participate in the fingerprint to
// detect dataset changes between successive streamed calls.
func applyStreamWindow[T any](items []T, identifier, firstKey, lastKey string, opts StreamOptions) (chunk []T, total int, nextCursor string, hasMore bool, err error) {
	total = len(items)
	fingerprint := streamFingerprint(identifier, total, firstKey, lastKey)

	offset := 0
	if opts.Cursor != "" {
		c, decErr := decodeStreamCursor(opts.Cursor)
		if decErr != nil {
			return nil, total, "", false, decErr
		}
		if c.Fingerprint != fingerprint || c.Total != total {
			return nil, total, "", false, fmt.Errorf("stream cursor invalidated: dataset changed since cursor was issued; restart streaming without cursor")
		}
		if c.Offset < 0 {
			c.Offset = 0
		}
		offset = c.Offset
	}

	if offset > total {
		offset = total
	}
	end := total
	if opts.ChunkSize > 0 && offset+opts.ChunkSize < end {
		end = offset + opts.ChunkSize
	}

	chunk = append([]T(nil), items[offset:end]...)
	hasMore = end < total
	if hasMore {
		nextCursor = encodeStreamCursor(streamCursor{Offset: end, Total: total, Fingerprint: fingerprint})
	}
	return chunk, total, nextCursor, hasMore, nil
}

// streamOrWindow is the standard helper for slice-returning tools. When
// opts.ChunkSize > 0 it returns the next streaming chunk plus continuation
// metadata; otherwise it falls back to legacy offset/limit/max_items
// pagination. items must already be in stable sorted order. keyFn yields a
// stable identity key for fingerprinting; identifier scopes the cursor to a
// logical dataset (e.g. "entrypoints:<dir>|<pattern>").
//
// When streaming, MaxItems is applied first as a global cap on the
// addressable dataset so successive chunks see a consistent total.
func streamOrWindow[T any](items []T, identifier string, keyFn func(T) string, opts QueryOptions) (chunk []T, total int, truncated, hasMore bool, nextCursor string, err error) {
	if opts.ChunkSize <= 0 {
		chunk, total, truncated = applyQueryWindow(items, opts)
		return chunk, total, truncated, false, "", nil
	}
	if opts.MaxItems > 0 && len(items) > opts.MaxItems {
		items = items[:opts.MaxItems]
	}
	var firstKey, lastKey string
	if len(items) > 0 {
		firstKey = keyFn(items[0])
		lastKey = keyFn(items[len(items)-1])
	}
	chunk, total, nextCursor, hasMore, err = applyStreamWindow(items, identifier, firstKey, lastKey, StreamOptions{Cursor: opts.Cursor, ChunkSize: opts.ChunkSize})
	truncated = hasMore || (opts.Cursor != "")
	return chunk, total, truncated, hasMore, nextCursor, err
}
