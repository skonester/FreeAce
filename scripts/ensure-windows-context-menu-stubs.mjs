import { mkdirSync, writeFileSync, existsSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("..", import.meta.url));
const outDir = path.join(root, "src-tauri", "windows", "shell", "out");
const force = process.argv.slice(2).includes("--force");
mkdirSync(outDir, { recursive: true });
for (const name of [
  "freeace_shell.dll",
  "freeace_extract_shell.dll",
  "FreeAceContextMenu.msix",
  "FreeAceExtractContextMenu.msix",
]) {
  const filePath = path.join(outDir, name);
  if (force || !existsSync(filePath)) {
    writeFileSync(filePath, "");
    console.log(
      `[ensure-windows-context-menu-stubs] ${force ? "reset" : "created"} empty ${filePath}`,
    );
  }
}
