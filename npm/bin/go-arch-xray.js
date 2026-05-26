#!/usr/bin/env node
"use strict";

const { spawnSync } = require("child_process");
const path = require("path");
const fs = require("fs");

const PLATFORM_MAP = {
  "win32-x64": { ext: ".exe" },
  "win32-arm64": { ext: ".exe" },
  "darwin-x64": { ext: "" },
  "darwin-arm64": { ext: "" },
  "linux-x64": { ext: "" },
  "linux-arm64": { ext: "" },
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
  const binPath = path.join(__dirname, `go-arch-xray${target.ext}`);

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
