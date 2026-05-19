# Advanced Usage & Configuration

## Memory note

If you still observe high RSS on very large monorepos, narrow your `package_patterns` to the modules you actually want to inspect rather than `./...`.

## Configuration

Go Architecture X-Ray can load project defaults from `.gax/config.yml` or
repo defaults from `.go-arch-xray.yml` in the active project root. Explicit
tool inputs always override config values, and `.gax/config.yml` takes
precedence over `.go-arch-xray.yml` when both exist. If no repo config exists,
tools keep today's built-in defaults, with `go.work`/`go.mod` discovery used
to suggest safer package patterns for multi-module repos.

Recommended workflow for AI clients:

1. Call `inspect_workspace_config` when analysis scope is unclear.
2. Call `suggest_workspace_config` to show a proposed config without changing files.
3. Call `init_workspace_config` only when the user explicitly asks to create the repo config.

Example `.go-arch-xray.yml`:

```yaml
version: 1
workspace:
  mode: go_work
  file: go.work
package_patterns:
  - ./services/api/...
  - ./libs/shared/...
cache_capacity: 2
output:
  max_items: 500
boundaries:
  - type: forbid
    from: example.com/app/internal/domain
    to: example.com/app/internal/infrastructure
complexity:
  min_cognitive: 15
  min_halstead_volume: 80
  max_maintainability_index: 55
  sort_by: maintainability
lifecycle:
  dedupe_mode: function_kind_field
  max_hops: 1000
orm:
  default_framework: gorm
  migration_dirs:
    - db/migrations
  table_inference: snake
sync:
  debounce: 2500ms
  check_interval: 30s
  auto_rebuild: true
embeddings:
  provider: local
  local:
    endpoint: http://localhost:11434/api/embeddings
    model: bge-m3
    timeout: 30s
  api:
    base_url: https://api.openai.com/v1
    model: text-embedding-3-small
    api_key_env: OPENAI_API_KEY
    timeout: 30s
  batch_size: 50
  chunk_size: 500
  dimension: 1024
```

User-local defaults are also supported at the OS config path, for example `%APPDATA%\go-arch-xray\config.yml` on Windows or `~/.config/go-arch-xray/config.yml` on Linux. Repo config should hold shared team policy; `.gax/config.yml` is best for project-local runtime overrides; user-local config is best for personal output preferences.

`GO_ARCH_XRAY_CACHE_CAPACITY` sets the initial in-memory workspace cache
capacity for the server process. Repo/user `cache_capacity` config takes
precedence for analysis requests that load configuration. Increase capacity
when one MCP session regularly alternates between several roots or
package-pattern sets; reduce it when memory is constrained.

## Shadow Workspace

Version 0.7.0 initializes a project-local `.gax/` directory for persistent
analysis metadata:

- `.gax/cache.db`: SQLite shadow cache with WAL enabled.
- `.gax/cache.db-wal` and `.gax/cache.db-shm`: SQLite WAL sidecar files.
- `.gax/config.yml`: project-local overrides that take precedence over
  `.go-arch-xray.yml`.
- `.gax/state.json`: background sync hash and progress state.

The SQLite cache now backs the fast-path MCP tools in 0.7.0. Package
dependencies, HTTP routes, gRPC endpoints, and `semantic_search` read from the
persisted store, while SSA-heavy tools still rely on the in-memory compute
router. The store is used to exercise schema migrations, snapshot writes,
symbol hashing, local embeddings, and background sync behavior.

`semantic_search` intentionally reads the shadow `code_symbols` index through
sqlite-vec for RAG context. Configure `embeddings.provider` as `local`, `api`, or `none`. The
`local` provider posts to the configured local HTTP embedding endpoint, while
the `api` provider posts to `<base_url>/embeddings` with a bearer token read
from `api_key_env`. Symbol hashes are compared before provider calls so
unchanged symbols keep their stored vectors during refreshes.

## Limitations

### Call Graph Precision

Call hierarchy and reachability analysis uses Class Hierarchy Analysis (CHA), which is an over-approximation:

- **Interface calls:** CHA conservatively assumes a concrete method can be called on any struct that implements the interface, even if runtime type checking would prevent the call.
- **Reflection:** Calls made via `reflect.Value.Call`, `reflect.Call`, or generated code (like protocol buffer stubs) are invisible to static analysis.
- **Plugins / CGO:** Dynamically loaded code, `//go:linkname`, and CGO interop are not tracked.
- **Anonymous functions:** Captured variables and closure behavior may not be fully represented.

### Concurrency Risk Heuristics

The `detect_concurrency_risks` tool uses bounded SSA access summaries:

- **False positives:** The analysis remains conservative around unknown roots, containers, reflection, CGO, unsafe, and unresolved calls.
- **False negatives:** Higher-level synchronization primitives (channels, `sync.Map`, external locks) are still not modeled directly.
- Use the risk results as a signal for manual review, not as proof of a race condition.

### SSA Scope

The Static Single Assignment (SSA) program is built only for explicitly loaded root packages:

- Transitive dependencies are loaded as type-only entries, not as full SSA programs.
- Functions in dependency packages (e.g., `net/http`) are not analyzed for internal structure—only for call-graph connectivity.
- For large monorepos, narrow your `package_patterns` to the modules you actually want to inspect rather than `./...`.

### Dead Code Detection

The `find_dead_code` tool defaults to `mode: "precision"`, which reports high-confidence unreferenced symbols with confidence, actionability, and evidence fields. Use `mode: "audit"` when you want the broader static inventory, including lower-confidence unreachable entrypoint chains:

- **Reflection:** Functions called via `reflect` are invisible and may be incorrectly flagged as dead.
- **Plugin patterns:** Code loaded at runtime or called through plugin interfaces will appear unreferenced.
- **Test-only usage:** If a function is only called from `*_test.go` files (which are not loaded into the analysis program), it may be flagged as dead.
- **Scope:** Use `scope_package_pattern` to report only the package subtree under review while loading a broader `package_pattern` for cross-package reachability.

Verify before deleting any symbols reported by `find_dead_code`.

### Orphaned Database Model Detection

The `find_orphaned_database_models` tool uses static evidence and should be
treated as a review signal. It recognizes direct ORM calls and bounded wrapper
forwarding patterns, including context-aware tenant sessions and repository
helpers that pass model destinations into GORM-style calls such as `Find`,
`Scan`, `Create`, `Updates`, and `AutoMigrate`.

When migration directories are configured, matching table evidence lowers
orphan confidence. A referenced model that is present in migrations is not
reported as `no_orm_usage` solely because its database calls are hidden behind
project-specific wrappers. Reflection, dynamic SQL construction, and models
stored only in `context.Context` remain conservative cases for manual review.

## Troubleshooting

### Empty Results

- **No packages found:** Your `package_pattern` or `package_patterns` may not match any Go packages. Try `./...` to scan from the repository root.
- **Multi-module workspace:** If you have a `go.work` file, use root-relative module patterns like `./services/api/...` and `./libs/shared/...` instead of `./...`. Call `inspect_workspace_config` or `suggest_workspace_config` for auto-detected patterns.
- **Generated code excluded:** For gRPC analysis, include generated `*.pb.go` or `*_grpc.pb.go` packages in your package pattern.

### Stale Cache

- If you've edited code and want fresh analysis, call `reload_workspace` with the same `root_path` and `package_pattern` you used before.
- The cache key combines `root_path` and `package_patterns`. If you change either parameter, the server will load fresh code.
- Call `clear_cache` with `all: true` to evict all cached programs if you're unsure of the cache state.

### Large Repositories

- High memory usage can occur on very large monorepos. Narrow your `package_patterns` to specific modules or subtrees.
- Use `package_patterns` with multiple specific patterns instead of broad `./...` patterns.
- Tune `cache_capacity` or `GO_ARCH_XRAY_CACHE_CAPACITY` (default 2) based on memory and cache-hit needs.
- Use `limit`/`offset` or `chunk_size`+`cursor` to paginate results instead of requesting all items at once.

### Slow Analysis

- First calls on a large repository will be slower as the workspace loads (go/packages → SSA). Subsequent calls use cached results.
- For repeated queries, use streaming (`chunk_size`+`cursor`) instead of repeated full queries with large `limit` values.
- Use `max_items` to cap the worst-case response size for large codebases.

## Notes

Diagnostic logs are written to stderr so stdout remains reserved for MCP protocol traffic. Business errors are returned as MCP tool errors with `isError: true`, allowing clients to correct inputs without treating the server transport as failed.
