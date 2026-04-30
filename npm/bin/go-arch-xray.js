#!/usr/bin/env node
// Thin launcher: locates the platform-specific go-arch-xray binary downloaded
// by install.js (or pointed to by GO_ARCH_XRAY_BIN) and execs it with the
// caller's argv, inheriting stdio and exit code.

"use strict";

const fs = require("fs");
const path = require("path");
const { spawnSync } = require("child_process");

const REPO = "HAYASAKA7/go-arch-xray";

function resolveBinary() {
  if (process.env.GO_ARCH_XRAY_BIN) {
    return process.env.GO_ARCH_XRAY_BIN;
  }
  const ext = process.platform === "win32" ? ".exe" : "";
  return path.join(__dirname, `go-arch-xray${ext}`);
}

const binPath = resolveBinary();

if (!fs.existsSync(binPath)) {
  process.stderr.write(
    `[go-arch-xray] binary not found at ${binPath}\n` +
      `The npm postinstall step did not run or failed.\n` +
      `Try one of:\n` +
      `  npm rebuild @hayasaka7/go-arch-xray\n` +
      `  download the asset manually from https://github.com/${REPO}/releases and set GO_ARCH_XRAY_BIN\n`
  );
  process.exit(1);
}

const result = spawnSync(binPath, process.argv.slice(2), { stdio: "inherit" });

if (result.error) {
  process.stderr.write(`[go-arch-xray] failed to spawn binary: ${result.error.message}\n`);
  process.exit(1);
}

if (result.signal) {
  process.kill(process.pid, result.signal);
}

process.exit(result.status ?? 1);
