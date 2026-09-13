// Builds the bundled `freeace` sidecar (Gleipnir, vendored at
// third_party/gleipnir/gleipnir.c) from source for the host platform, since
// unlike prepare-7z.js there is no official prebuilt binary to download.
// zlib is a hard `#include <zlib.h>` dependency everywhere: Linux/macOS link
// the system zlib (`-lz`), while Windows compiles the vendored copy at
// third_party/zlib/ alongside gleipnir.c. Builds only the host/arch, like
// prepare-7z.js without `--all` -- there is no cross-compilation support.

import fs from "fs";
import path from "path";
import { spawnSync } from "child_process";

const root = process.cwd();
const gleipnirSource = path.join(root, "third_party", "gleipnir", "gleipnir.c");
const zlibDir = path.join(root, "third_party", "zlib");
const outDir = path.join(root, "src-tauri", "binaries");

const ZLIB_SOURCES = [
  "adler32.c",
  "compress.c",
  "crc32.c",
  "deflate.c",
  "gzclose.c",
  "gzlib.c",
  "gzread.c",
  "gzwrite.c",
  "infback.c",
  "inffast.c",
  "inflate.c",
  "inftrees.c",
  "trees.c",
  "uncompr.c",
  "zutil.c",
];

function targetTripleForHost() {
  if (process.platform === "win32") {
    return process.arch === "arm64"
      ? "aarch64-pc-windows-msvc"
      : "x86_64-pc-windows-msvc";
  }
  if (process.platform === "darwin") {
    return process.arch === "arm64"
      ? "aarch64-apple-darwin"
      : "x86_64-apple-darwin";
  }
  if (process.platform === "linux") {
    return process.arch === "arm64"
      ? "aarch64-unknown-linux-gnu"
      : "x86_64-unknown-linux-gnu";
  }
  throw new Error(`Unsupported host platform: ${process.platform}`);
}

function which(command) {
  const result = spawnSync(
    process.platform === "win32" ? "where" : "which",
    [command],
    { stdio: "pipe" },
  );
  return result.status === 0;
}

function run(command, args, options = {}) {
  console.log(`+ ${command} ${args.join(" ")}`);
  const result = spawnSync(command, args, { stdio: "inherit", ...options });
  if (result.error) {
    throw new Error(`${command} failed to start: ${result.error.message}`);
  }
  if (result.status !== 0) {
    throw new Error(`${command} exited with code ${result.status}`);
  }
}

function buildUnix(outputPath) {
  const cc = process.env.CC ?? "cc";
  if (!which(cc)) {
    throw new Error(
      `No C compiler ("${cc}") found. Install one (see build-setup.md) or set $CC.`,
    );
  }
  const archFlags =
    process.arch === "x64" && process.platform !== "darwin"
      ? ["-march=x86-64-v2"]
      : [];
  run(cc, [
    "-O3",
    "-funroll-loops",
    "-ffp-contract=off",
    ...archFlags,
    "-o",
    outputPath,
    gleipnirSource,
    "-lz",
    "-lm",
    "-lpthread",
  ]);
}

function buildMacUniversal(outputPath) {
  const cc = process.env.CC ?? "clang";
  run(cc, [
    "-O3",
    "-funroll-loops",
    "-ffp-contract=off",
    "-arch",
    "x86_64",
    "-arch",
    "arm64",
    "-o",
    outputPath,
    gleipnirSource,
    "-lz",
    "-lm",
    "-lpthread",
  ]);
}

function buildWindows(outputPath) {
  // clang auto-targets the installed MSVC toolchain (cl.exe/link.exe) on
  // Windows and is what this was developed and verified against; it also
  // accepts the same GCC-style flags gleipnir.c's own build scripts use.
  const cc = process.env.CC ?? (which("clang") ? "clang" : "cc");
  if (!which(cc)) {
    throw new Error(
      `No C compiler ("${cc}") found. Install LLVM/clang plus the Visual Studio Build Tools (see build-setup.md), or MinGW-w64, and set $CC if needed.`,
    );
  }
  const zlibSources = ZLIB_SOURCES.map((name) => path.join(zlibDir, name));
  for (const source of zlibSources) {
    if (!fs.existsSync(source)) {
      throw new Error(`Missing vendored zlib source: ${source}`);
    }
  }
  run(cc, [
    "-O2",
    "-w",
    "-o",
    outputPath,
    gleipnirSource,
    ...zlibSources,
    "-I",
    zlibDir,
  ]);
}

function verifyBinary(outputPath) {
  const result = spawnSync(outputPath, ["--version"], { stdio: "pipe" });
  if (result.status !== 0) {
    throw new Error(
      `Built freeace binary failed "--version" check: ${result.stderr?.toString() || result.error?.message || "unknown error"}`,
    );
  }
  const banner = result.stdout.toString();
  if (!banner.startsWith("Gleipnir ")) {
    throw new Error(`Unexpected freeace --version banner: ${banner}`);
  }
  console.log(banner.split("\n")[0]);
}

function main() {
  if (!fs.existsSync(gleipnirSource)) {
    throw new Error(`Missing vendored Gleipnir source: ${gleipnirSource}`);
  }
  fs.mkdirSync(outDir, { recursive: true });

  const triple = targetTripleForHost();
  const exeSuffix = process.platform === "win32" ? ".exe" : "";
  // Named after the upstream Gleipnir engine, not "freeace": the main app
  // binary is also named freeace(.exe) now, and Tauri installs external
  // sidecars under their externalBin basename next to it, so reusing
  // "freeace" here would collide with and clobber the app's own executable.
  const outputPath = path.join(outDir, `gleipnir-${triple}${exeSuffix}`);

  if (process.platform === "win32") {
    buildWindows(outputPath);
  } else if (process.platform === "darwin") {
    buildMacUniversal(outputPath);
    // Same one universal binary under every arch-specific sidecar name
    // Tauri's macOS bundling expects, mirroring prepare-7z.js's mac mapping.
    for (const altTriple of [
      "x86_64-apple-darwin",
      "aarch64-apple-darwin",
      "universal-apple-darwin",
    ]) {
      const altPath = path.join(outDir, `gleipnir-${altTriple}`);
      if (altPath !== outputPath) fs.copyFileSync(outputPath, altPath);
    }
  } else {
    buildUnix(outputPath);
  }

  if (process.platform !== "win32") {
    fs.chmodSync(outputPath, 0o755);
  }
  verifyBinary(outputPath);
  console.log(`freeace sidecar ready: ${outputPath}`);
}

main();
