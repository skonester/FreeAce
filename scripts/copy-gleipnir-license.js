#!/usr/bin/env node

// Copies the vendored Gleipnir license into public/ so the in-app
// Open-Source Licenses viewer (src/licenses.ts) can display it, the same way
// copy-7zip-license.js does for the bundled 7-Zip binaries. Gleipnir
// (third_party/gleipnir/) is GPL-3.0-or-later and is bundled only as a
// separate sidecar binary invoked as a subprocess (mere aggregation under
// GPLv3 section 5), never linked into this MPL-2.0 application.

import { copyFileSync, mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const source = join(root, "third_party", "gleipnir", "LICENSE.md");
const destination = join(root, "public", "gleipnir-license.txt");

mkdirSync(dirname(destination), { recursive: true });
copyFileSync(source, destination);
console.log(`[licenses:gleipnir] Wrote ${destination}`);
