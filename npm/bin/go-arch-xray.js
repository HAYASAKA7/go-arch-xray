#!/usr/bin/env node
"use strict";

const { spawnSync } = require("child_process");
const path = require("path");
const os = require("os");
const fs = require("fs");

// Resolve package.json from the package root (not bin directory)
const pkgPath = path.join(__dirname, "..", "package.json");
const pkg = require(pkgPath);
const VERSION = pkg.version;
const TAG = `v${VERSION}`;

const PLATFORM_MAP = {
  "win32-x64": { goos: "windows", goarch: "amd64", ext: ".exe" },
  "win32-arm64": { goos: "windows", goarch: "arm64", ext: ".exe" },
  "darwin-x64": { goos: "darwin", goarch: "amd64", ext: "" },
  "darwin-arm64": { goos: "darwin", goarch: "arm64", ext: "" },
  "linux-x64": { goos: "linux", goarch: "amd64", ext: "" },
  "linux-arm64": { goos: "linux", goarch: "arm64", ext: "" },
};

function log(msg) {
  process.stderr.write(`[go-arch-xray] ${msg}\n`);
}

function detectTarget() {
  const key = `${process.platform}-${process.arch}`;
  const target = PLATFORM_MAP[key];
  if (!target) {
    throw new Error(
      `Unsupported platform ${key}. Supported: ${Object.keys(PLATFORM_MAP).join(", ")}.\n` +
        `Build from source: https://github.com/HAYASAKA7/go-arch-xray#build-from-source`
    );
  }
  return target;
}

function getBinaryPath() {
  // Check for GO_ARCH_XRAY_BIN override first
  if (process.env.GO_ARCH_XRAY_BIN) {
    return process.env.GO_ARCH_XRAY_BIN;
  }

  const target = detectTarget();
  const binaryName = `go-arch-xray-${target.goos}-${target.goarch}${target.ext}`;
  const binDir = path.join(__dirname, "bin");
  const binPath = path.join(binDir, binaryName);

  if (!fs.existsSync(binPath)) {
    throw new Error(
      `Binary not found at ${binPath}.\n` +
        `Run 'npm rebuild @hayasaka7/go-arch-xray' to re-download, or set GO_ARCH_XRAY_BIN to an existing binary path.`
    );
  }

  return binPath;
}

function main() {
  const binPath = getBinaryPath();
  const args = process.argv.slice(2);

  const result = spawnSync(binPath, args, {
    stdio: "inherit",
  });

  if (result.error) {
    log(`Failed to launch: ${result.error.message}`);
    process.exit(1);
  }

  process.exit(result.status ?? 0);
}

main();