import assert from "node:assert/strict";
import test from "node:test";
import { buildWindowsUpdaterManifests } from "./write-windows-updater-manifests.js";

const base = {
  version: "0.6.4",
  repo: "skonester/FreeAce",
  pubDate: "2026-10-25T00:00:00Z",
  readSignature: (name) => `sig-for-${name}\n`,
};

test("writes one manifest per arch with nsis and fallback keys", () => {
  const { manifests, missing } = buildWindowsUpdaterManifests({
    ...base,
    files: [
      "FreeAce_0.6.4_x64-setup.exe",
      "FreeAce_0.6.4_x64-setup.exe.sig",
      "FreeAce_0.6.4_arm64-setup.exe",
      "FreeAce_0.6.4_arm64-setup.exe.sig",
      "freeace-windows-x64-portable.exe",
    ],
  });
  assert.deepEqual(missing, []);
  assert.deepEqual(Object.keys(manifests).sort(), [
    "latest-windows-aarch64.json",
    "latest-windows-x86_64.json",
  ]);
  const x64 = manifests["latest-windows-x86_64.json"];
  assert.equal(x64.version, "0.6.4");
  assert.deepEqual(Object.keys(x64.platforms), [
    "windows-x86_64-nsis",
    "windows-x86_64",
  ]);
  assert.equal(
    x64.platforms["windows-x86_64"].url,
    "https://github.com/skonester/FreeAce/releases/download/v0.6.4/FreeAce_0.6.4_x64-setup.exe",
  );
  assert.equal(
    x64.platforms["windows-x86_64"].signature,
    "sig-for-FreeAce_0.6.4_x64-setup.exe.sig",
  );
});

test("reports missing signatures instead of publishing a partial set", () => {
  const { manifests, missing } = buildWindowsUpdaterManifests({
    ...base,
    files: [
      "FreeAce_0.6.4_x64-setup.exe",
      "FreeAce_0.6.4_x64-setup.exe.sig",
      "FreeAce_0.6.4_arm64-setup.exe",
    ],
  });
  assert.deepEqual(missing, ["FreeAce_0.6.4_arm64-setup.exe.sig"]);
  assert.deepEqual(Object.keys(manifests), ["latest-windows-x86_64.json"]);
});
