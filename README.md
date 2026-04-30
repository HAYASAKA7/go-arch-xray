# Go Architecture X-Ray MCP

Go Architecture X-Ray is a Model Context Protocol server for inspecting Go codebases from an AI client. It runs over stdio and keeps a process-scoped LRU cache (default 2 entries) of analyzed programs for the life of the MCP session.

## What's New (0.4.6)

- **MCP-first instruction policy**: server guidance now explicitly prioritizes MCP analysis tools over generic text/file search when understanding repository structure and relationships.
- **Refactor pre-check/post-verify policy**: server guidance now explicitly requires MCP-based impact pre-check before refactors and MCP-based post-verification after refactors.
- **Tool descriptions aligned with policy**: key topology tools are now labeled as primary MCP-first tools to improve agent tool-selection behavior.

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

### Workspace Management

- `reload_workspace`: Invalidates and reloads the cached `go/packages` and SSA analysis for a root path and package pattern.
- `cache_status`: Returns LRU cache occupancy and per-entry metadata (package count, function count).
- `clear_cache`: Clears cache entries by `root_path`/`package_pattern` key, or clears all entries with `all: true`.
- `list_entrypoints`: Lists `main` functions, `init` functions, and goroutine spawn sites across loaded packages.
- `list_http_routes`: Scans source files for HTTP route registrations (net/http, gin, chi, gorilla/mux). Returns route method, path, handler, framework, and source location for literal-path routes.

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

Use the absolute path to the compiled binary.

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

## Release Workflow

Maintainers can publish a release by pushing a tag that starts with `v`:

```bash
git tag v0.4.6
git push origin v0.4.6
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

## Notes

Diagnostic logs are written to stderr so stdout remains reserved for MCP protocol traffic. Business errors are returned as MCP tool errors with `isError: true`, allowing clients to correct inputs without treating the server transport as failed.
