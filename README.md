# Go Architecture X-Ray MCP

Go Architecture X-Ray is a Model Context Protocol server for inspecting Go codebases from an AI client. It runs over stdio and keeps a process-scoped in-memory analysis cache for the life of the MCP session.

## Tools

- `get_interface_topology`: Finds structs that implement a target interface. Supports value and pointer receivers, embedding, package-qualified interface names, stdlib filtering, source locations, and context anchors.
- `analyze_call_hierarchy`: Builds a CHA static call hierarchy from a function. Traversal is capped at 3 hops and labels edges as `Static`, `Interface`, or `Goroutine`.
- `trace_struct_lifecycle`: Uses SSA to report struct instantiation, field mutation, and interface handoff points.
- `detect_concurrency_risks`: Heuristically flags struct fields mutated inside goroutines without visible mutex or `sync/atomic` protection.
- `reload_workspace`: Invalidates and reloads the cached `go/packages` and SSA analysis for a root path and package pattern.
- `get_package_dependencies`: Returns direct package import dependencies for architecture boundary inspection.

## Build

```powershell
go build ./...
```

Release-style Windows binary:

```powershell
go build -trimpath -ldflags "-s -w" -o go-arch-xray.exe .
```

## MCP Host Configuration

Use the absolute path to the compiled binary.

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

## Common Inputs

Most tools accept:

- `root_path`: Root directory of the Go project. Defaults to the server working directory.
- `package_pattern`: Go package pattern. Defaults to `./...`.

Example `get_interface_topology` input:

```json
{
  "root_path": "D:\\Projects\\ExampleGoProject",
  "package_pattern": "./...",
  "interface_name": "example.com/project/internal/api.Service",
  "include_stdlib": false
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
