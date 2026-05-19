# Go Architecture X-Ray MCP

Go Architecture X-Ray is a Model Context Protocol server for inspecting Go codebases from an AI client. It runs locally over stdio and maintains a process-scoped, lazily-initialized LRU cache of analyzed programs. The cache defaults to 2 entries and can be tuned with `GO_ARCH_XRAY_CACHE_CAPACITY`. The server features an automated background sweeper that evicts idle caches after 15 minutes to minimize system memory footprint during the life of the MCP session.

Version 0.7.0 adds a project-local `.gax/` workspace and SQLite-backed analysis store. The server creates `.gax/cache.db` automatically, writes flattened analysis snapshots and symbol metadata in the background, and serves fast-path queries from the persisted store while SSA-heavy analysis still uses the in-memory compute router.

## Features Overview

- **Call Graph & Reachability:** Analyze call hierarchies and trace execution paths across your Go codebase.
- **Import Graph & Architecture:** Map package dependencies, detect cycles, and enforce architectural boundaries.
- **Struct Analysis:** Discover interface implementations, trace struct lifecycles, and identify concurrency risks with bounded SSA access summaries, lockset tracking, and atomic awareness.
- **Code Quality & Refactor Signals:** Detect precision-first dead-code candidates, find duplicate methods, identify orphaned database models with wrapper/session-aware ORM usage evidence, and compute complexity metrics (cyclomatic, cognitive, Halstead).
- **RAG Symbol Search:** Use `semantic_search` to retrieve indexed code symbols and source snippets from the project-local shadow index for retrieval-augmented context.
- **Shadow Index & Background Sync:** Maintain a project-local `.gax/cache.db` shadow store with WAL mode, file hashes, AST symbols, deterministic local embeddings, and background rebuild queue coordination.
- **Workspace Management:** Dynamically reload workspaces, manage caches, inspect configurations, and list entrypoints (HTTP routes, gRPC endpoints, etc.).

## Shadow SQLite Index

The `.gax/` directory is created inside the analyzed project root:

```text
.gax/
  cache.db
  cache.db-wal
  cache.db-shm
  config.yml
  state.json
```

In 0.7.0 the persisted store backs the fast-path tools. It validates the schema, WAL behavior, snapshot export, symbol hashing, and background sync pipeline while package dependency, HTTP route, gRPC endpoint, and semantic search queries read from SQLite. SSA-heavy tools still use the in-memory compute router.

The `semantic_search` MCP tool reads the persisted `code_symbols` index through
a sqlite-vec vector table and returns symbol-level source snippets for RAG
context. The fast-path analyzers for package dependencies, HTTP routes, and
gRPC endpoints also read from SQLite in 0.7.0.

Set `embeddings.provider` to `local` for an Ollama-style local HTTP endpoint,
`api` for an OpenAI-compatible `/embeddings` endpoint, or `none` to fall back
to deterministic local vectors. The shadow index compares symbol hashes before
embedding calls, so unchanged symbols keep their stored vectors during refreshes.

Background sync can be tuned in `.go-arch-xray.yml` or `.gax/config.yml`:

```yaml
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
  dimension: 1024
```

Set `sync.auto_rebuild: false` to keep the SQLite store initialized but disable the polling watcher.

## Focused Candidate Reports

For broad workspaces, load enough packages for accurate analysis with `package_pattern`
or `package_patterns`, then narrow noisy candidate-style reports with
`scope_package_pattern`. This is supported by `find_dead_code`,
`find_duplicate_methods`, and `find_orphaned_database_models`.

```json
{
  "root_path": "D:\\Projects\\ExampleGoProject",
  "package_pattern": "./...",
  "scope_package_pattern": "./pkg/sync/...",
  "mode": "precision"
}
```

## Agent Workflow Guidance

The server exposes MCP-native prompts and resources for clients that support
them, plus a `suggest_analysis_workflow` tool for tool-first agents. These
cover onboarding, refactor prechecks, cleanup audits, API surface inventory,
concurrency review, and architecture checks without requiring client-specific
skill files.

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

## Runtime Tuning

Set `GO_ARCH_XRAY_CACHE_CAPACITY` to keep more loaded workspaces warm when
clients alternate between broad and narrow package patterns:

```bash
GO_ARCH_XRAY_CACHE_CAPACITY=6 go-arch-xray
```

Higher values reduce repeated cold `go/packages` and SSA loads at the cost of
additional memory. Repo/user `cache_capacity` config still takes precedence for
analysis requests that load configuration.

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

## Documentation

For more detailed information, please refer to the following documents:

- [Tools Reference](docs/tools-reference.md): Detailed parameter lists, streaming/pagination instructions, and graph diagram exports.
- [Advanced Usage & Configuration](docs/advanced-usage.md): Repo/user configuration files, memory limits, limitations, and troubleshooting.
- [Contributing](CONTRIBUTING.md): Instructions for building from source and release workflows.
