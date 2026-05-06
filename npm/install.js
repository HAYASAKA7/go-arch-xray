#!/usr/bin/env node
// Downloads the platform-specific go-arch-xray binary from the matching
// GitHub Release and extracts it into ./bin. Runs automatically as the
// npm `postinstall` step.
//
// Override the binary location entirely by setting GO_ARCH_XRAY_BIN to an
// absolute path; in that case this script does nothing.
//
// Skip the download in CI / offline installs with `npm install --ignore-scripts`.
// The bin shim will print a clear remediation message at first run.

"use strict";

const fs = require("fs");
const path = require("path");
const os = require("os");
const { spawnSync } = require("child_process");
const { pipeline } = require("stream/promises");
const { Readable } = require("stream");

const REPO = "HAYASAKA7/go-arch-xray";
const pkg = require("./package.json");
const VERSION = pkg.version;
const TAG = `v${VERSION}`;

const PLATFORM_MAP = {
  "win32-x64": { goos: "windows", goarch: "amd64", ext: ".exe", archive: "zip" },
  "win32-arm64": { goos: "windows", goarch: "arm64", ext: ".exe", archive: "zip" },
  "darwin-x64": { goos: "darwin", goarch: "amd64", ext: "", archive: "tar.gz" },
  "darwin-arm64": { goos: "darwin", goarch: "arm64", ext: "", archive: "tar.gz" },
  "linux-x64": { goos: "linux", goarch: "amd64", ext: "", archive: "tar.gz" },
  "linux-arm64": { goos: "linux", goarch: "arm64", ext: "", archive: "tar.gz" },
};

function log(msg) {
  process.stderr.write(`[go-arch-xray] ${msg}\n`);
}

function fail(msg, code = 1) {
  log(`ERROR: ${msg}`);
  process.exit(code);
}

function detectTarget() {
  const key = `${process.platform}-${process.arch}`;
  const target = PLATFORM_MAP[key];
  if (!target) {
    fail(
      `Unsupported platform ${key}. Supported: ${Object.keys(PLATFORM_MAP).join(", ")}.\n` +
        `Build from source: https://github.com/${REPO}#build-from-source`
    );
  }
  return { key, ...target };
}

function binaryPath(target) {
  const binDir = path.join(__dirname, "bin");
  const name = `go-arch-xray${target.ext}`;
  return { binDir, binPath: path.join(binDir, name) };
}

function archiveInternalName(target) {
  return `go-arch-xray-${target.goos}-${target.goarch}${target.ext}`;
}

function archiveAssetName(target) {
  return `go-arch-xray-${TAG}-${target.goos}-${target.goarch}.${target.archive}`;
}

function assetUrl(target) {
  return `https://github.com/${REPO}/releases/download/${TAG}/${archiveAssetName(target)}`;
}

async function download(url, destFile) {
  log(`downloading ${url}`);
  const res = await fetch(url, { redirect: "follow" });
  if (!res.ok) {
    throw new Error(`HTTP ${res.status} ${res.statusText} for ${url}`);
  }
  await pipeline(Readable.fromWeb(res.body), fs.createWriteStream(destFile));
}

function ensureTar() {
  const probe = spawnSync("tar", ["--version"], { stdio: "ignore" });
  if (probe.status !== 0) {
    fail(
      "`tar` was not found on PATH. Install GNU tar / bsdtar, or set " +
        "GO_ARCH_XRAY_BIN to an existing binary path."
    );
  }
}

function extract(archivePath, destDir) {
  ensureTar();
  // Modern bsdtar (Windows 10 1803+, macOS, Linux) handles both .tar.gz and .zip via -xf.
  const result = spawnSync("tar", ["-xf", archivePath, "-C", destDir], {
    stdio: "inherit",
  });
  if (result.status !== 0) {
    fail(`tar extraction failed (exit ${result.status})`);
  }
}

// Cross-volume safe replacement for fs.renameSync. On Windows, npm's tmpdir
// commonly lives on C: while node_modules can be on D:, which makes rename
// fail with EXDEV. Fall back to copy + unlink in that case.
function moveFile(src, dest) {
  try {
    fs.renameSync(src, dest);
    return;
  } catch (err) {
    if (err && err.code !== "EXDEV") throw err;
  }
  fs.copyFileSync(src, dest);
  try {
    fs.unlinkSync(src);
  } catch {
    /* best effort */
  }
}

async function main() {
  if (process.env.GO_ARCH_XRAY_BIN) {
    log(`GO_ARCH_XRAY_BIN is set; skipping download.`);
    return;
  }

  const target = detectTarget();
  const { binDir, binPath } = binaryPath(target);

  // Check for version mismatch on upgrades
  const versionFilePath = path.join(binDir, "version.txt");
  let needsDownload = true;
  if (fs.existsSync(binPath) && fs.existsSync(versionFilePath)) {
    const installedVersion = fs.readFileSync(versionFilePath, "utf8").trim();
    if (installedVersion === VERSION) {
      log(`binary already present at ${binPath}; skipping download.`);
      needsDownload = false;
    } else {
      log(`upgrading from ${installedVersion} to ${VERSION}`);
    }
  } else if (fs.existsSync(binPath)) {
    log(`binary exists but no version file; re-downloading ${VERSION}`);
  }

  if (!needsDownload) {
    return;
  }

  fs.mkdirSync(binDir, { recursive: true });

  // Stage the download next to the install target so the final move is on the
  // same volume; falls back to OS temp if that directory is not writable.
  let tmpRoot;
  try {
    tmpRoot = fs.mkdtempSync(path.join(binDir, ".download-"));
  } catch {
    tmpRoot = fs.mkdtempSync(path.join(os.tmpdir(), "go-arch-xray-"));
  }
  const tmpDir = tmpRoot;
  const archivePath = path.join(tmpDir, archiveAssetName(target));

  try {
    await download(assetUrl(target), archivePath);
    extract(archivePath, tmpDir);

    const extractedName = archiveInternalName(target);
    const extractedPath = path.join(tmpDir, extractedName);
    if (!fs.existsSync(extractedPath)) {
      fail(
        `expected '${extractedName}' inside archive but it was not found. ` +
          `Open an issue: https://github.com/${REPO}/issues`
      );
    }

    moveFile(extractedPath, binPath);
    if (process.platform !== "win32") {
      fs.chmodSync(binPath, 0o755);
    }
    // Write version file for upgrade detection
    fs.writeFileSync(versionFilePath, VERSION, "utf8");
    log(`installed ${binPath}`);
  } catch (err) {
    fail(
      `failed to install binary: ${err && err.message ? err.message : err}\n` +
        `URL: ${assetUrl(target)}\n` +
        `You can download the asset manually, extract it, and either drop the binary at\n` +
        `  ${binPath}\n` +
        `or set GO_ARCH_XRAY_BIN to its absolute path.`
    );
  } finally {
    try {
      fs.rmSync(tmpDir, { recursive: true, force: true });
    } catch {
      /* best effort */
    }
  }
}

main().catch((err) => fail(err && err.stack ? err.stack : String(err)));
