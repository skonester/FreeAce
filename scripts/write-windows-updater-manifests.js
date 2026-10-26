#!/usr/bin/env node
/**
 * Write latest-windows-<arch>.json updater manifests for the Windows CI
 * release, from the NSIS installers and the .sig files Tauri emits beside
 * them when TAURI_SIGNING_PRIVATE_KEY is set. Shape matches
 * testdata/updater/latest-windows-*.json and the endpoints in tauri.conf.json.
 *
 * Usage:
 *   node scripts/write-windows-updater-manifests.js <releaseDir> <version> <owner/repo>
 */

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const ARCHES = [
  { arch: "x86_64", installerArch: "x64" },
  { arch: "aarch64", installerArch: "arm64" },
];

export function buildWindowsUpdaterManifests({
  files,
  readSignature,
  version,
  repo,
  pubDate,
}) {
  const tag = `v${version}`;
  const manifests = {};
  const missing = [];
  for (const { arch, installerArch } of ARCHES) {
    const installer = `FreeAce_${version}_${installerArch}-setup.exe`;
    if (!files.includes(installer)) {
      missing.push(installer);
      continue;
    }
    if (!files.includes(`${installer}.sig`)) {
      missing.push(`${installer}.sig`);
      continue;
    }
    const entry = {
      url: `https://github.com/${repo}/releases/download/${tag}/${encodeURIComponent(installer)}`,
      signature: readSignature(`${installer}.sig`).trim(),
    };
    manifests[`latest-windows-${arch}.json`] = {
      version,
      pub_date: pubDate,
      platforms: {
        [`windows-${arch}-nsis`]: entry,
        [`windows-${arch}`]: entry,
      },
    };
  }
  return { manifests, missing };
}

function main() {
  const [releaseDir, version, repo] = process.argv.slice(2);
  if (!releaseDir || !version || !repo) {
    console.error(
      "usage: write-windows-updater-manifests.js <releaseDir> <version> <owner/repo>",
    );
    process.exit(1);
  }
  const { manifests, missing } = buildWindowsUpdaterManifests({
    files: fs.readdirSync(releaseDir),
    readSignature: (name) =>
      fs.readFileSync(path.join(releaseDir, name), "utf8"),
    version,
    repo,
    pubDate: new Date().toISOString().replace(/\.\d{3}Z$/, "Z"),
  });
  // A partial set would leave one architecture's clients failing their
  // update check while the other succeeds, so refuse rather than publish it.
  if (missing.length > 0) {
    console.error(`Missing updater inputs: ${missing.join(", ")}`);
    process.exit(1);
  }
  for (const [name, manifest] of Object.entries(manifests)) {
    fs.writeFileSync(
      path.join(releaseDir, name),
      `${JSON.stringify(manifest, null, 2)}\n`,
    );
    console.log(`+ ${name}`);
  }
}

if (process.argv[1] === fileURLToPath(import.meta.url)) main();
