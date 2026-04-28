# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.3.0] - 2026-04-28

### Added
- `find_callers` tool: reverse call hierarchy — returns the incoming caller
  tree for any function up to `max_depth` hops (default 3, max 8) using
  the cached CHA call graph. Edges are labelled `Static`, `Interface`, or
  `Goroutine`.
- `find_call_path` tool: BFS reachability query from one function to
  another over the CHA call graph. Returns up to `max_paths` (default 20)
  distinct paths; each path includes step-by-step `CallEdge` entries.
  Configurable `max_depth` (default 8, max 12).
- `detect_import_cycles` tool: Tarjan SCC over the loaded package import
  graph. Returns all cyclic strongly-connected components with member
  package paths. Nodes outside the loaded set (e.g. stdlib transitive
  deps) are silently excluded.
- `find_reverse_dependencies` tool: inverts the import graph to return
  which packages within the loaded program import a given `target_package`.
  Supports `include_transitive: true` for the full dependent closure.
- `cache_status` tool: returns LRU cache size, capacity, and per-entry
  metadata (patterns, package count, function count).
- `clear_cache` tool: clears a specific cache entry by `root_path` +
  `package_pattern`, or all entries with `all: true`.
- `trace_struct_lifecycle` now accepts three output-control parameters:
  - `dedupe_mode`: `none` (default) / `function_field` / `function_kind_field`
    to collapse repeated hops from the same function/field combination.
  - `max_hops`: cap on the number of hops returned (default 500, max 20 000).
  - `summary`: when `true`, includes aggregated counts by kind/field.
  Output also includes `total_before_truncate` and `truncated` metadata
  fields so callers know when results were cut.
- `Workspace.Status()` returning cache size, capacity, and
  `[]CacheRecord` entry metadata.
- `Workspace.Clear(dir, pattern)` for targeted cache invalidation.
- `Workspace.ClearAll()` to evict every cached program.
- Internal `findSCCs` helper (Tarjan algorithm) exposed at package level
  for unit testing without requiring real import cycles.

## [0.2.0] - 2026-04-28

### Added
- Multi-pattern queries: every tool now accepts `package_patterns: string[]`
  (or a comma-separated `package_pattern`) so a single request can scan
  across `./internal/...`, `./pkg/...`, etc. without reloading.
- `analyzer.SplitPatterns` helper and order-invariant cache key for
  multi-pattern loads.
- LRU cache for `Workspace` (default capacity 2, configurable via
  `Workspace.SetCapacity`) with automatic eviction of the least-recently
  used loaded program.
- `Workspace.Stats()` exposing current cache size and capacity.
- `LoadedProgram.CallGraph()` lazily builds and caches the CHA call graph
  per loaded program for reuse across `analyze_call_hierarchy` requests.
- `LoadedProgram.RootPaths` and `LoadedProgram.Patterns` for downstream
  filtering and diagnostics.
- `reload_workspace` response now includes `package_patterns`,
  `cache_size`, and `cache_capacity`.
- `AllLoadedPackages` helper that walks roots + transitive imports.
- New tests covering multi-pattern loading, order-invariant cache keys,
  LRU eviction, CHA reuse, and root-only SSA function set.

### Changed
- SSA program is built only for the requested (root) packages via
  `ssautil.Packages` + `ssa.BareInits | ssa.InstantiateGenerics`;
  transitive dependencies stay as type-only entries. Drastically lowers
  memory on large dependency graphs.
- After SSA build, the loader drops `Syntax`, `TypesInfo`, `GoFiles`,
  `OtherFiles`, `EmbedFiles`, `EmbedPatterns`, `IgnoredFiles`, and trims
  `CompiledGoFiles` to the first entry to release `go/packages` memory.
- `SSAFuncs` is filtered to root-package functions only; lifecycle and
  concurrency analyzers no longer iterate stdlib wrappers.
- `get_interface_topology` now resolves fully-qualified interface names
  (`pkgpath.Name`) across all transitively loaded packages and removes
  the previous recursive-walk allocation pattern.
- Default-pattern handling consolidated into `Workspace.GetOrLoad` so
  individual analyzers no longer duplicate the `./...` fallback.
- Server version bumped to `0.2.0`.

### Removed
- README warning about memory usage risk; the issue has been mitigated.

## [0.1.0] - 2026-04

### Added
- Initial MCP server with six tools: `get_interface_topology`,
  `analyze_call_hierarchy`, `trace_struct_lifecycle`,
  `detect_concurrency_risks`, `reload_workspace`,
  `get_package_dependencies`.
- Process-scoped in-memory analysis cache keyed by `(root, pattern)`.
- GitHub Actions release workflow producing cross-platform binaries.
