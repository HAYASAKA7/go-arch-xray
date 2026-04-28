# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
