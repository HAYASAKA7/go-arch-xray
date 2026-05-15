# Go Architecture X-Ray MCP

Go Architecture X-Ray is a Model Context Protocol server for inspecting Go codebases from an AI client. It runs locally over stdio and maintains a process-scoped, lazily-initialized LRU cache (default 2 entries) of analyzed programs. The server features an automated background sweeper that evicts idle caches after 15 minutes to minimize system memory footprint during the life of the MCP session.

## Features Overview

- **Call Graph & Reachability:** Analyze call hierarchies and trace execution paths across your Go codebase.
- **Import Graph & Architecture:** Map package dependencies, detect cycles, and enforce architectural boundaries.
- **Struct Analysis:** Discover interface implementations, trace struct lifecycles, and identify concurrency risks with bounded SSA access summaries, lockset tracking, and atomic awareness.
- **Code Quality & Refactor Signals:** Detect precision-first dead-code candidates, find duplicate methods, identify orphaned database models, and compute complexity metrics (cyclomatic, cognitive, Halstead).
- **Workspace Management:** Dynamically reload workspaces, manage caches, inspect configurations, and list entrypoints (HTTP routes, gRPC endpoints, etc.).

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
