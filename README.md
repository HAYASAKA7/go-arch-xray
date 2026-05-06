# Go Architecture X-Ray MCP

Go Architecture X-Ray is a Model Context Protocol server for inspecting Go codebases from an AI client. It runs over stdio and keeps a process-scoped LRU cache (default 2 entries) of analyzed programs for the life of the MCP session.

## Memory note

If you still observe high RSS on very large monorepos, narrow your `package_patterns` to the modules you actually want to inspect rather than `./...`.

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
- `detect_concurrency_risks`: Heuristically flags struct fields mutated inside goroutines without visible mutex or `sync/atomic` protection.

### Code Quality & Refactor Signals

- `find_dead_code`: Reports unexported functions and methods that are unreferenced or unreachable from any program entrypoint via the CHA call graph. Pass `include_exported: true` to also audit exported symbols (useful for internal modules). Result includes caveats — CHA cannot see reflection, plugins, cgo, or `//go:linkname`.
- `find_duplicate_methods`: Groups together functions and methods whose signature and normalized body match across the workspace. Bodies are hashed after whitespace normalization and comment stripping. Tune `min_body_lines` (default 3) to control the noise floor.
- `compute_complexity_metrics`: Reports per-function cyclomatic complexity, cognitive complexity, body lines, max nesting, Halstead metrics, and `maintainability_index`. Use it before refactors, during code review, for onboarding, and when prioritizing tests. Use `min_cyclomatic`, `min_cognitive`, `min_halstead_volume`, `max_maintainability_index`, and `sort_by` to focus results; set `include_packages: true` for package-level debt scans. Prefer `sort_by: "halstead_volume"` or `"halstead_effort"` for dense expression/operator-heavy code, and `sort_by: "maintainability"` to review lowest maintainability scores first. Complexity, Halstead, and maintainability metrics are structural ranking signals, not proof of performance, security, or correctness problems.

### Workspace Management

- `reload_workspace`: Invalidates and reloads the cached `go/packages` and SSA analysis for a root path and package pattern.
- `cache_status`: Returns LRU cache occupancy and per-entry metadata (package count, function count).
- `clear_cache`: Clears cache entries by `root_path`/`package_pattern` key, or clears all entries with `all: true`.
- `inspect_workspace_config`: Shows the repo config path, user-local config path, auto-detected `go.work`/`go.mod` defaults, and the effective config used by tools.
- `suggest_workspace_config`: Returns a proposed `.go-arch-xray.yml` from `go.work`/`go.mod` discovery without writing files.
- `init_workspace_config`: Writes `.go-arch-xray.yml` in the repo root from discovered defaults. It does not overwrite an existing file unless `overwrite: true` is passed.
- `list_entrypoints`: Lists `main` functions, `init` functions, and goroutine spawn sites across loaded packages.
- `list_http_routes`: Scans source files for HTTP route registrations (net/http, gin, chi, gorilla/mux, echo, fiber, fasthttp/router). Returns route method, path, handler, framework, and source location for literal-path routes. Supports cursor streaming for large route tables.
- `list_grpc_endpoints`: Discovers generated grpc-go `ServiceDesc` methods and `Register<Service>Server` call sites in loaded Go packages. Returns service, method, full method path, RPC type (`unary`, `client_stream`, `server_stream`, `bidi_stream`), handler, proto metadata, registration status, implementations, and source locations. Include generated `*.pb.go` or `*_grpc.pb.go` packages in the package pattern. Pagination and streaming cover endpoint rows and registration rows together; `total` and `total_registrations` report each full unpaged count.

## Configuration

Go Architecture X-Ray can load repo defaults from `.go-arch-xray.yml` in the active project root. Explicit tool inputs always override config values. If no repo config exists, tools keep today's built-in defaults, with `go.work`/`go.mod` discovery used to suggest safer package patterns for multi-module repos.

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
```

User-local defaults are also supported at the OS config path, for example `%APPDATA%\go-arch-xray\config.yml` on Windows or `~/.config/go-arch-xray/config.yml` on Linux. Repo config should hold shared team policy; user-local config is best for personal output preferences.

## Install From GitHub Releases

Tagged releases build binaries for:

- Windows amd64: `go-arch-xray-<tag>-windows-amd64.zip`
- Windows arm64: `go-arch-xray-<tag>-windows-arm64.zip`
- macOS Intel: `go-arch-xray-<tag>-darwin-amd64.tar.gz`
- macOS Apple Silicon: `go-arch-xray-<tag>-darwin-arm64.tar.gz`
- Linux amd64: `go-arch-xray-<tag>-linux-amd64.tar.gz`
- Linux arm64: `go-arch-xray-<tag>-linux-arm64.tar.gz`

Download the archive for your platform from the GitHub Releases page, extract it, and use the extracted binary path in your MCP host configuration.

On macOS/Linux, make the binary executable if needed:

```bash
chmod +x ./go-arch-xray-*
```

## Install From npm

A thin Node launcher is published as [`@hayasaka7/go-arch-xray`](https://www.npmjs.com/package/@hayasaka7/go-arch-xray).
On install, a `postinstall` script downloads the matching binary from the
corresponding GitHub Release. Use it directly with `npx`:

```bash
npx -y @hayasaka7/go-arch-xray
```

Or install globally:

```bash
npm install -g @hayasaka7/go-arch-xray
go-arch-xray
```

Set `GO_ARCH_XRAY_BIN=/absolute/path/to/binary` to skip the download and
point the launcher at a pre-installed binary (useful for air-gapped
environments).

## Build From Source

```powershell
go build ./...
```

Release-style binary for macOS/Linux:

```bash
go build -trimpath -ldflags "-s -w" -o go-arch-xray .
```

Release-style binary for Windows:

```powershell
go build -trimpath -ldflags "-s -w" -o go-arch-xray.exe .
```

## MCP Host Configuration

If you installed via npm, use the `npx` command configuration shown in the next section so MCP hosts don't need an absolute path.

eg. You can install for Claude Code with:

```text
claude mcp add go-arch-xray -- npx -y @hayasaka7/go-arch-xray
```

Use the absolute path to the compiled binary.

eg. Claude Code command configuration:

Windows:

```text
claude mcp add go-arch-xray "Disk:\\path\\to\\go-arch-xray.exe"
```

macOS/Linux:

```text
claude mcp add go-arch-xray "/path/to/go-arch-xray"
```

Windows:

```json
{
  "mcpServers": {
    "go-arch-xray": {
      "command": "D:\\Projects\\MCPDev\\go-arch-xray.exe",
      "args": []
    }
  }
}
```

macOS/Linux:

```json
{
  "mcpServers": {
    "go-arch-xray": {
      "command": "/usr/local/bin/go-arch-xray",
      "args": []
    }
  }
}
```

If you downloaded a release asset, the extracted binary name includes the target platform, for example:

```json
{
  "mcpServers": {
    "go-arch-xray": {
      "command": "/Users/you/bin/go-arch-xray-darwin-arm64",
      "args": []
    }
  }
}
```

If you installed via npm, use `npx` so MCP hosts don't need an absolute path:

```json
{
  "mcpServers": {
    "go-arch-xray": {
      "command": "npx",
      "args": ["-y", "@hayasaka7/go-arch-xray"]
    }
  }
}
```

## Release Workflow

Maintainers can publish a release by pushing a tag that starts with `v`:

```bash
git tag v0.5.0
git push origin v0.5.0
```

The GitHub Actions workflow runs tests, cross-compiles release binaries for Windows, macOS, and Linux, packages them, and attaches them to the GitHub Release.

## Common Inputs

Most tools accept:

- `root_path`: Root directory of the Go project. Defaults to the server working directory.
- `package_pattern`: Single Go package pattern. Also accepts a comma-separated list. Defaults to `./...`.
- `package_patterns`: Array of Go package patterns. Merged with `package_pattern` (deduplicated). Use this for multi-module / multi-subtree scans in one request.

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
`find_duplicate_methods`, `compute_complexity_metrics`):

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

## Limitations

### Call Graph Precision

Call hierarchy and reachability analysis uses Class Hierarchy Analysis (CHA), which is an over-approximation:

- **Interface calls:** CHA conservatively assumes a concrete method can be called on any struct that implements the interface, even if runtime type checking would prevent the call.
- **Reflection:** Calls made via `reflect.Value.Call`, `reflect.Call`, or generated code (like protocol buffer stubs) are invisible to static analysis.
- **Plugins / CGO:** Dynamically loaded code, `//go:linkname`, and CGO interop are not tracked.
- **Anonymous functions:** Captured variables and closure behavior may not be fully represented.

### Concurrency Risk Heuristics

The `detect_concurrency_risks` tool uses static heuristics:

- **False positives:** The analysis flags field mutations inside goroutines that lack visible mutex or `sync/atomic` protection. Some code may use higher-level synchronization (channels, atomic-free patterns, or external guarantees) that the analysis cannot see.
- **False negatives:** The analysis only checks for explicit `sync.Mutex` / `sync.RWMutex` / `sync/atomic` patterns. Other synchronization primitives (channels, `sync.Map`, external locks) are not recognized.
- Use the risk results as a signal for manual review, not as proof of a race condition.

### SSA Scope

The Static Single Assignment (SSA) program is built only for explicitly loaded root packages:

- Transitive dependencies are loaded as type-only entries, not as full SSA programs.
- Functions in dependency packages (e.g., `net/http`) are not analyzed for internal structure—only for call-graph connectivity.
- For large monorepos, narrow your `package_patterns` to the modules you actually want to inspect rather than `./...`.

### Dead Code Detection

The `find_dead_code` tool reports unexported symbols with zero inbound callers or unreachable entrypoint chains:

- **Reflection:** Functions called via `reflect` are invisible and may be incorrectly flagged as dead.
- **Plugin patterns:** Code loaded at runtime or called through plugin interfaces will appear unreferenced.
- **Test-only usage:** If a function is only called from `*_test.go` files (which are not loaded into the analysis program), it may be flagged as dead.

Verify before deleting any symbols reported by `find_dead_code`.

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
- Reduce `cache_capacity` (default 2) if memory is constrained.
- Use `limit`/`offset` or `chunk_size`+`cursor` to paginate results instead of requesting all items at once.

### Slow Analysis

- First calls on a large repository will be slower as the workspace loads (go/packages → SSA). Subsequent calls use cached results.
- For repeated queries, use streaming (`chunk_size`+`cursor`) instead of repeated full queries with large `limit` values.
- Use `max_items` to cap the worst-case response size for large codebases.

## Notes

Diagnostic logs are written to stderr so stdout remains reserved for MCP protocol traffic. Business errors are returned as MCP tool errors with `isError: true`, allowing clients to correct inputs without treating the server transport as failed.
