# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.5.2] - 2026-04-30

### Added

- HTTP route discovery now recognizes additional frameworks: Echo
  (`Any`, `CONNECT`, `TRACE`), Fiber (`All`, `Connect`, `Trace`), and
  fasthttp/router (`ANY`, `CONNECT`, `TRACE`). Framework attribution
  uses receiver type information when available so vendored or aliased
  routers are classified correctly even when method names overlap with
  other frameworks.
- Optional graph diagram export for `get_package_dependencies`,
  `analyze_call_hierarchy`, `check_architecture_boundaries`, and
  `find_reverse_dependencies`. Pass `export: "mermaid"`, `"dot"`, or
  `"json-graph"` to receive a renderable diagram in a new `diagram`
  result field. The diagram only includes nodes/edges from the current
  pagination/streaming window so payload size stays bounded. Architecture
  boundary diagrams highlight violating edges (dashed) and source packages
  ("violation" class) for visual emphasis. Default behavior is unchanged
  when `export` is omitted.

## [0.5.1] - 2026-04-30

### Fixed

- npm `install.js` failed with `EXDEV: cross-device link not permitted`
  when the system temp directory and `node_modules` lived on different
  Windows volumes (e.g. temp on `C:`, project on `D:`). The downloader
  now stages the archive on the same volume as the install target when
  possible, and falls back to a copy + unlink when the final move would
  cross a volume boundary.
- Server version bumped to `0.5.1`.

## [0.5.0] - 2026-04-30

### Added

- npm distribution under `@hayasaka7/go-arch-xray`. The package ships a
  small Node launcher and a `postinstall` script that downloads the
  matching native binary (Windows / macOS / Linux × x64 / arm64) from
  the corresponding GitHub Release. MCP hosts can now configure the
  server with `npx -y @hayasaka7/go-arch-xray` instead of managing a
  local binary path.
- `GO_ARCH_XRAY_BIN` environment variable lets users point the launcher
  at a pre-installed binary, skipping the download (useful for
  air-gapped environments and corporate package mirrors).
- GitHub Actions release workflow now publishes the npm package with
  npm provenance after the GitHub Release assets are uploaded.

### Changed

- Go module path renamed to `github.com/HAYASAKA7/go-arch-xray` to match
  the canonical repository owner.
- Server version bumped to `0.5.0`.

## [0.4.9] - 2026-04-30

### Changed

- MCP server `Instructions` now include an explicit output-size policy that
  steers AI clients to prefer cursor-based streaming (`chunk_size` +
  `cursor`) over large `max_items`/`limit` values for slice-returning
  tools, with explicit recovery rules for `truncated:true` responses and
  `stream cursor invalidated` errors.
- Server version bumped to `0.4.9`.

## [0.4.8] - 2026-04-30

### Added

- Cursor-based streaming extended to every slice-returning analysis tool:
  `get_interface_topology`, `get_package_dependencies`, `find_callers`,
  `find_reverse_dependencies`, `check_architecture_boundaries`,
  `list_entrypoints`, and `list_http_routes` now accept `chunk_size` +
  `cursor` and emit `chunk_size`, `next_cursor`, and `has_more` in the
  response. Same fingerprint-based invalidation semantics as the streaming
  introduced for `analyze_call_hierarchy` and `trace_struct_lifecycle` in
  `0.4.7`.
- Internal `streamOrWindow` helper unifies streaming and pagination across
  all slice-returning tools so behavior stays consistent.
- Server version bumped to `0.4.8`.

## [0.4.7] - 2026-04-30

### Added

- Cursor-based streaming for `analyze_call_hierarchy` and
  `trace_struct_lifecycle` via new `chunk_size` and `cursor` parameters.
  When `chunk_size > 0`, results are returned in fixed-size chunks together
  with an opaque `next_cursor` token; passing the token back as `cursor`
  resumes the stream. The cursor binds to a fingerprint of the underlying
  dataset so mid-stream changes (e.g. a workspace reload) are detected and
  surfaced as an error rather than silently producing inconsistent output.
- Streaming is fully backward compatible: when `chunk_size` is omitted,
  behavior and response shape are unchanged.
- Server version bumped to `0.4.7`.

## [0.4.6] - 2026-04-30

### Changed

- MCP server instructions now explicitly enforce an MCP-first workflow for
  repository understanding: prefer MCP analysis tools before generic
  text/file search when possible.
- MCP server instructions now explicitly require refactor pre-check and
  post-verification using MCP analysis tools.
- Key MCP tool descriptions now identify primary tools for dependency, call
  flow, reverse-call impact, call reachability, entrypoint, and HTTP route
  analysis to improve tool-selection behavior.
- Server version bumped to `0.4.6`.

## [0.4.5] - 2026-04-29

### Changed

- `check_architecture_boundaries` now uses import-location metadata cached on
  `LoadedProgram` (`importLocs`) that is extracted during workspace load.
  This removes repeated import-block re-parsing in the normal path.
- `list_http_routes` now uses route metadata cached on `LoadedProgram`
  (`httpRoutes`) extracted from package syntax during workspace load.
  This removes repeated source parsing on subsequent route queries.
- Workspace loader now captures both import locations and HTTP routes before
  syntax/file-list memory trimming, preserving analysis correctness while
  keeping memory optimizations.
- Server version bumped to `0.4.5`.

### Fixed

- Route discovery no longer depends on post-trim `CompiledGoFiles` scanning,
  which could miss routes declared outside the preserved file subset.

## [0.4.4] - 2026-04-29

### Added

- `get_interface_topology` regression test for narrow-pattern fallback:
  fully-qualified dependency interface lookup now has explicit coverage.

### Changed

- `get_interface_topology` now retries interface resolution with workspace
  fallback patterns (`./...` and go.work-derived module patterns) when the
  initial lookup fails with interface-not-found errors under narrow package
  patterns.
- Server version bumped to `0.4.4`.

## [0.4.3] - 2026-04-29

### Added

- New workspace parser coverage for `go.work` `use` forms (block and
  inline syntax) used by fallback pattern expansion.
- New query-window regression test ensuring empty windows serialize as empty
  non-nil slices.

### Changed

- Function lookup in call-graph tools now uses staged matching:
  exact and receiver-qualified matching, then case-insensitive fallback, then
  ambiguity refinement with candidate listing.
- `analyze_call_hierarchy`, `find_callers`, and `find_call_path` now share a
  common function-lookup fallback path that retries broader patterns only for
  true "not found" cases.
- Workspace fallback pattern discovery now reads `go.work` module `use`
  directives and tries additional module patterns (`./submodule/...`) in
  addition to `./...`.
- Filesystem-like package patterns are normalized relative to `root_path`
  before load/cache keying, improving cache hit correctness and package loading
  consistency.

### Fixed

- Empty paginated output now returns `[]` instead of `null`.
- Stdlib filtering for dependencies/interface scans now checks module metadata
  (`Module == nil`) to avoid treating local dotless-module imports as stdlib.
- `go.work` parsing now correctly skips `use (` block openers and avoids
  invalid fallback patterns.
- Server version bumped to `0.4.3`.

## [0.4.2] - 2026-04-29

### Changed

- MCP server now advertises an `Instructions` string to clients, granting
  automatic permission to call all tools without requiring per-call user
  confirmation. This removes the need for AI clients to ask before invoking
  any analysis tool.
- Server version bumped to `0.4.2`.

## [0.4.1] - 2026-04-29

### Added

- Unified pagination and output controls (`limit`, `offset`, `max_items`) are now
  supported by all remaining high-volume tools:
  - `get_interface_topology` — also gains `summary` support, returning
    `TopologySummary` with `total_implementors`.
  - `find_callers`
  - `find_reverse_dependencies`
  - `check_architecture_boundaries`
  - `list_entrypoints`
  - `list_http_routes`
- All result types for the above tools now include consistent truncation
  metadata fields: `total_before_truncate` and `truncated`.
- `WithOptions` variants added for every analyzer function (`CheckArchitectureBoundariesWithOptions`,
  `FindCallersWithOptions`, `FindReverseDependenciesWithOptions`,
  `GetInterfaceTopologyWithOptions`, `ListEntrypointsWithOptions`,
  `ListHTTPRoutesWithOptions`). Original signatures are preserved and delegate
  to the new variants with empty `QueryOptions{}`.

### Changed

- Server version bumped to `0.4.1`.

## [0.4.0] - 2026-04-28

### Added

- `check_architecture_boundaries` tool: evaluates every package's import
  graph against a configurable ruleset. Supports three rule types:
  - `forbid` — any import from a `from`-matching package to a `to`-matching
    package is a violation.
  - `allow_only` — packages matching `from` may only import packages
    matching `to` (intra-project; stdlib is always permitted).
  - `allow_prefix` — packages matching `from` may only import packages
    whose path starts with `to` (intra-project; stdlib is always permitted).
  Each violation includes `from`, `import`, `rule`, source `file`, `line`,
  and `context_anchor`. Pattern matching supports exact paths or
  trailing-`/` prefix patterns.
- `list_entrypoints` tool: scans the SSA program for `main` functions,
  `init` functions, and goroutine spawn sites (`go` statements). Returns
  kind, function name, package, and source location for each entrypoint.
- `list_http_routes` tool: scans source files via AST for HTTP route
  registrations from `net/http`, gin (`r.GET`, `r.POST`, …), chi
  (`r.Get`, `r.Post`, …), and gorilla/mux. Returns HTTP method, path,
  handler name, inferred framework, and source location. Dynamic paths
  (non-literal first argument) are silently skipped.
- `analyzer.BoundaryRule` / `BoundaryViolation` / `BoundaryResult` types.
- `analyzer.Entrypoint` / `EntrypointsResult` types.
- `analyzer.HTTPRoute` / `HTTPRoutesResult` types.

### Changed

- Server version bumped to `0.4.0`.

## [0.3.1] - 2026-04-28

### Added

- Shared query/output controls for high-volume tools:
  - `limit`
  - `offset`
  - `summary`
  - `max_items`
- `analyze_call_hierarchy` now supports optional aggregate summary output
  (`total_edges`, grouped counters by call type/caller/callee).
- `get_package_dependencies` now supports optional aggregate summary output
  (`total_packages`, `total_imports`, per-package import counts).

### Changed

- Output throttling/pagination behavior is now standardized across call
  hierarchy, dependency, and lifecycle-style large result sets.
- Truncation metadata is now consistently populated with
  `total_before_truncate` and `truncated` where applicable.
- Server version bumped to `0.3.1`.

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
