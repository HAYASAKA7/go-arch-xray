# Tools Reference

## Tools

### Call Graph & Reachability

- `analyze_call_hierarchy`: Builds a forward CHA call hierarchy from a function. Capped at 3 hops; labels edges as `Static`, `Interface`, or `Goroutine`.
- `find_callers`: Finds the incoming caller tree for a target function. Configurable depth up to 8 hops.
- `find_call_path`: BFS over the CHA call graph to find call paths from one function to another. Returns up to `max_paths` distinct paths; each path has step-by-step `CallEdge` entries.

### Import Graph & Architecture

- `get_package_dependencies`: Returns direct package import dependencies for architecture boundary inspection.
- `find_reverse_dependencies`: Returns packages that import a given target package. Optionally includes the transitive dependent closure.
- `detect_import_cycles`: Detects import cycles in the loaded package graph using Tarjan SCC. Returns all cyclic strongly-connected components.
- `check_architecture_boundaries`: Evaluates packages against a configurable ruleset (`forbid`, `allow_only`, `allow_prefix`). Intra-project violations are reported with file/line locations. Stdlib is always permitted in allow-type rules.

### Struct Analysis

- `get_interface_topology`: Finds structs that implement a target interface. Supports value and pointer receivers, embedding, package-qualified interface names, stdlib filtering, source locations, and context anchors.
- `trace_struct_lifecycle`: Uses SSA to report struct instantiation, field mutation, and interface handoff points. Supports `dedupe_mode`, `max_hops`, and `summary` output controls.
- `detect_concurrency_risks`: Uses bounded SSA access summaries, goroutine capture tracking, helper-call propagation, lockset analysis, and atomic awareness to report concurrent field access risks. It preserves precise memory roots where possible, reports lower-confidence risks for unknown roots/containers/unresolved dynamic calls, summarizes repeated diagnostics by default, and can include raw diagnostics with `include_diagnostics: true`.

### Code Quality & Refactor Signals

- `find_dead_code`: Reports precision-first dead-code candidates. Default `mode: "precision"` returns high-confidence unreferenced functions and methods with confidence, actionability, and evidence fields; `mode: "audit"` returns the broader static inventory with labels, including unreachable caller-chain findings. Pass `include_exported: true` to also audit exported symbols (useful for internal modules). Use `scope_package_pattern` to report only a package subtree while keeping broader workspace reachability loaded. Result includes caveats - CHA cannot see reflection, plugins, cgo, or `//go:linkname`.
- `find_duplicate_methods`: Groups together functions and methods whose signature and normalized body match across the workspace. Bodies are hashed after whitespace normalization and comment stripping. Tune `min_body_lines` (default 3) to control the noise floor. Use `scope_package_pattern` to report duplicate groups that touch a package subtree while still loading the broader workspace.
- `find_orphaned_database_models`: Detects database models that are defined but never initialized or used in queries. Currently supports GORM (`gorm:"..."` tagged structs), ent, sqlx, bun, and sqlc. Reports models with confidence, actionability, evidence, and summary metadata so clients can distinguish deletion candidates from verify-first findings. Includes table name inference, cross-referencing with migration files, nested model destination matching, and bounded wrapper forwarding so context-aware tenant sessions and repository helpers are less likely to produce false positives. Use `scope_package_pattern` to report only models in the package subtree under review.
- `compute_complexity_metrics`: Reports per-function cyclomatic complexity, cognitive complexity, body lines, max nesting, Halstead metrics, and `maintainability_index`. Use it before refactors, during code review, for onboarding, and when prioritizing tests. Use `min_cyclomatic`, `min_cognitive`, `min_halstead_volume`, `max_maintainability_index`, and `sort_by` to focus results; set `include_packages: true` for package-level debt scans. Prefer `sort_by: "halstead_volume"` or `"halstead_effort"` for dense expression/operator-heavy code, and `sort_by: "maintainability"` to review lowest maintainability scores first. Complexity, Halstead, and maintainability metrics are structural ranking signals, not proof of performance, security, or correctness problems.

### Workspace Management

- `suggest_analysis_workflow`: Returns a compact MCP-first workflow for Go repository onboarding, refactor prechecks, cleanup audits, API surface inventory, concurrency review, or architecture checks. Call this before reading files when the user asks to check, inspect, understand, review, audit, map, or refactor a Go repository.
- `reload_workspace`: Invalidates and reloads the cached `go/packages` and SSA analysis for a root path and package pattern.
- `cache_status`: Returns LRU cache occupancy and per-entry metadata (package count, function count, and last access time).
- `clear_cache`: Clears cache entries by `root_path`/`package_pattern` key, or clears all entries with `all: true`.
- `inspect_workspace_config`: Shows the repo config path, user-local config path, auto-detected `go.work`/`go.mod` defaults, and the effective config used by tools.
- `suggest_workspace_config`: Returns a proposed `.go-arch-xray.yml` from `go.work`/`go.mod` discovery without writing files.
- `init_workspace_config`: Writes `.go-arch-xray.yml` in the repo root from discovered defaults. It does not overwrite an existing file unless `overwrite: true` is passed.
- `list_entrypoints`: Lists `main` functions, `init` functions, and goroutine spawn sites across loaded packages.
- `list_http_routes`: Scans source files for HTTP route registrations (net/http, gin, chi, gorilla/mux, echo, fiber, fasthttp/router). Returns route method, path, handler, framework, and source location for literal-path routes. Supports cursor streaming for large route tables.
- `list_grpc_endpoints`: Discovers generated grpc-go `ServiceDesc` methods and `Register<Service>Server` call sites in loaded Go packages. Returns service, method, full method path, RPC type (`unary`, `client_stream`, `server_stream`, `bidi_stream`), handler, proto metadata, registration status, implementations, and source locations. Include generated `*.pb.go` or `*_grpc.pb.go` packages in the package pattern. Pagination and streaming cover endpoint rows and registration rows together; `total` and `total_registrations` report each full unpaged count.

## Prompts and Resources

The server also exposes MCP-native prompt/resource guidance so clients do not
need client-specific skill files to discover good tool workflows.

Prompts:

- `go_onboarding`
- `go_refactor_precheck`
- `go_cleanup`
- `go_api_surface_inventory`
- `go_concurrency_review`
- `go_architecture_check`

Resources:

- `go-arch-xray://agent-guide`
- `go-arch-xray://workflow/onboarding`
- `go-arch-xray://workflow/refactor_precheck`
- `go-arch-xray://workflow/cleanup`
- `go-arch-xray://workflow/api_surface_inventory`
- `go-arch-xray://workflow/concurrency_review`
- `go-arch-xray://workflow/architecture_check`

Clients that support prompts/resources can surface these directly. Clients
that mostly use tools can call `suggest_analysis_workflow` to get the same
workflow guidance as structured tool output.

## Common Inputs

Most tools accept:

- `root_path`: Root directory of the Go project. Defaults to the server working directory.
- `package_pattern`: Single Go package pattern. Also accepts a comma-separated list. Defaults to `./...`.
- `package_patterns`: Array of Go package patterns. Merged with `package_pattern` (deduplicated). Use this for multi-module / multi-subtree scans in one request.

The process-scoped workspace cache defaults to 2 entries. Set
`GO_ARCH_XRAY_CACHE_CAPACITY` to keep more root/pattern combinations warm in
long-running MCP sessions.

Candidate-report tools also accept:

- `scope_package_pattern`: Narrows reported candidates to packages matching this pattern while still loading the broader workspace from `package_pattern` / `package_patterns`. Supported by `find_dead_code`, `find_duplicate_methods`, and `find_orphaned_database_models`. Use this when an AI client is reviewing one package, such as `./pkg/sync/...`, but needs cross-package references from `./...` to avoid local-only false positives.

High-volume tools also accept:

- `limit`: Maximum number of items returned.
- `offset`: Pagination start index.
- `summary`: Return aggregate counts in addition to detailed entries.
- `max_items`: Hard cap safety limit on returned items.
- `chunk_size`: Page size for cursor-based streaming. When set (>0), the
  response returns at most `chunk_size` items along with `next_cursor` and
  `has_more`. Iterate by passing `next_cursor` back as `cursor` until
  `has_more` is `false`.
- `cursor`: Opaque continuation token returned by a previous streamed
  response. Do not modify or construct manually.

### Streaming vs. Pagination

For slice-returning tools (`get_interface_topology`, `get_package_dependencies`,
`find_callers`, `find_reverse_dependencies`, `check_architecture_boundaries`,
`list_entrypoints`, `list_http_routes`, `analyze_call_hierarchy`,
`trace_struct_lifecycle`, `list_grpc_endpoints`, `find_dead_code`,
`find_duplicate_methods`, `find_orphaned_database_models`,
`compute_complexity_metrics`):

- Prefer cursor-based streaming (`chunk_size` 20-50 + `cursor`) over large
  `max_items`/`limit` values. Large single payloads can overflow MCP transport
  buffers and LLM context windows.
- The server caps every chunk at **50 items** by default to protect AI
  context budgets — values above 50 are silently clamped. Override with the
  `GO_ARCH_XRAY_MAX_CHUNK_SIZE` environment variable when running against
  transports/clients that can handle larger responses.
- When a non-streaming response returns `truncated: true` with a large
  `total_before_truncate`, retry with `chunk_size` instead of bumping
  `max_items`.
- Each streamed response carries a fingerprint of the underlying dataset.
  If the workspace is reloaded mid-iteration, the next call returns a
  `stream cursor invalidated` error. Restart the stream **without** `cursor`;
  do not attempt to repair the token.

The MCP server `Instructions` field tells AI clients to follow this policy
automatically, so most clients will pick streaming without prompting.

### Graph Diagram Export

`get_package_dependencies`, `analyze_call_hierarchy`,
`check_architecture_boundaries`, and `find_reverse_dependencies` accept an
optional `export` parameter:

- `mermaid` — Markdown-renderable Mermaid diagram (`graph LR` / `graph TD`).
  Boundary violations and roots/targets are tagged with classes
  (`violation`, `root`, `target`) for visual emphasis.
- `dot` — Graphviz `digraph` source, suitable for `dot -Tsvg`.
- `json-graph` — Plain `{nodes, edges}` JSON for custom visualizations.

When `export` is provided the response gains a `diagram` field populated with
the rendered string. Diagrams reflect only the current pagination/streaming
window, so payload size stays bounded by the same `limit`/`max_items`/
`chunk_size` controls. Default behavior (no `export`) is unchanged.

Example boundary check with diagram:

```json
{
  "root_path": "D:\\Projects\\ExampleGoProject",
  "package_pattern": "./...",
  "rules": [{"type": "forbid", "from": "example.com/project/api/", "to": "example.com/project/repo/"}],
  "export": "mermaid"
}
```

Multi-pattern example for `get_interface_topology`:

```json
{
  "root_path": "D:\\Projects\\ExampleGoProject",
  "package_patterns": ["./internal/...", "./pkg/api/..."],
  "interface_name": "example.com/project/internal/api.Service",
  "include_stdlib": false
}
```

Legacy single-pattern example:

```json
{
  "root_path": "D:\\Projects\\ExampleGoProject",
  "package_pattern": "./...",
  "interface_name": "example.com/project/internal/api.Service",
  "include_stdlib": false
}
```

Example `find_call_path` input:

```json
{
  "root_path": "D:\\Projects\\ExampleGoProject",
  "package_pattern": "./...",
  "from_function": "HandleRequest",
  "to_function": "db.Query",
  "max_depth": 8,
  "max_paths": 5
}
```

Example scoped dead-code scan:

```json
{
  "root_path": "D:\\Projects\\ExampleGoProject",
  "package_pattern": "./...",
  "scope_package_pattern": "./pkg/sync/...",
  "mode": "precision"
}
```

Example `find_reverse_dependencies` input:

```json
{
  "root_path": "D:\\Projects\\ExampleGoProject",
  "package_pattern": "./...",
  "target_package": "example.com/project/internal/core",
  "include_transitive": true
}
```

Example `analyze_call_hierarchy` input:

```json
{
  "root_path": "D:\\Projects\\ExampleGoProject",
  "package_pattern": "./...",
  "function_name": "Run",
  "max_depth": 3
}
```
