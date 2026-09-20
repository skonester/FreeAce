#!/usr/bin/env node

// Copies the vendored Gleipnir license into public/ so the in-app
// Open-Source Licenses viewer (src/licenses.ts) can display it, the same way
// copy-7zip-license.js does for the bundled 7-Zip binaries. Gleipnir
// (third_party/gleipnir/) is GPL-3.0-or-later and is bundled as a separate
// sidecar binary invoked as a subprocess. FreeAce itself is GPL-3.0-or-later
// (see LICENSE), so this separation is an architectural choice rather than a
// licensing requirement.

import { copyFileSync, mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const source = join(root, "third_party", "gleipnir", "LICENSE.md");
const destination = join(root, "public", "gleipnir-license.txt");

mkdirSync(dirname(destination), { recursive: true });
copyFileSync(source, destination);
console.log(`[licenses:gleipnir] Wrote ${destination}`);
