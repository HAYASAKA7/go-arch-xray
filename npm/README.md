# go-arch-xray

npm distribution of [`go-arch-xray`](https://github.com/HAYASAKA7/go-arch-xray) — a
Model Context Protocol (MCP) server for static analysis of Go codebases.

This package contains a small Node launcher. On install, a `postinstall` script
downloads the matching native binary (Windows / macOS / Linux × x64 / arm64)
from the corresponding [GitHub Release](https://github.com/HAYASAKA7/go-arch-xray/releases).

## Install

```bash
npm install -g @hayasaka7/go-arch-xray
```

Or use it without installing:

```bash
npx -y @hayasaka7/go-arch-xray
```

## MCP host configuration

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

## Environment variables

- `GO_ARCH_XRAY_BIN` — absolute path to a pre-installed binary. When set,
  `postinstall` is skipped and the launcher invokes that binary directly.
  Useful for air-gapped environments or corporate package mirrors.

## Troubleshooting

- **Install ran with `--ignore-scripts`**: download blocked. Run
  `npm rebuild @hayasaka7/go-arch-xray` once, or set `GO_ARCH_XRAY_BIN`.
- **Behind a firewall**: download the matching release asset manually from
  <https://github.com/HAYASAKA7/go-arch-xray/releases>, extract it, and set
  `GO_ARCH_XRAY_BIN` to the binary path.
- **Unsupported platform**: build from source —
  see <https://github.com/HAYASAKA7/go-arch-xray#build-from-source>.

## License

MIT — see [LICENSE](./LICENSE).
