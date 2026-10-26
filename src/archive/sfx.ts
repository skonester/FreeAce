import { state } from "../state";

/**
 * Self-extracting output is an archive behind a Windows stub: 7-Zip's GUI
 * module for 7z, or FreeAce's own extractor for .freeace. Either way it exists
 * only for fresh creates on Windows. The webview emits only the bare `-sfx`
 * marker: the backend supplies the hash-checked stub, because 7-Zip resolves
 * a relative module name against its working directory first.
 */
export const SFX_SWITCH = "-sfx";

const SFX_TOGGLE_IDS = ["sfx", "basic-sfx"] as const;
const OUTPUT_PATH_IDS = ["output-path", "basic-output-path"] as const;

function input(id: string): HTMLInputElement | null {
  return document.getElementById(id) as HTMLInputElement | null;
}

export function sfxPlatformSupported(): boolean {
  return document.body.classList.contains("platform-windows");
}

const SFX_FORMATS: Record<string, string> = {
  "7z": ".7z",
  freeace: ".freeace",
};

export function sfxAllowed(format: string, updateMode: boolean): boolean {
  return sfxPlatformSupported() && format in SFX_FORMATS && !updateMode;
}

function currentFormat(): string {
  return (
    (document.getElementById("format") as HTMLSelectElement | null)?.value ?? ""
  );
}

/**
 * True only when the Power toggle (which Basic mirrors into) is checked and
 * the current form can produce SFX output, so stale toggle state never leaks.
 */
export function isSfxRequested(): boolean {
  const toggle = input("sfx");
  if (!toggle?.checked || toggle.disabled) return false;
  return sfxAllowed(currentFormat(), input("update-mode")?.checked ?? false);
}

/** Swap the archive suffix for `.exe` and back; other formats pass through. */
export function withSfxExtension(
  path: string,
  sfx: boolean,
  format: string,
): string {
  const archiveSuffix = SFX_FORMATS[format];
  if (!archiveSuffix) return path;
  return sfx
    ? path.replace(/(?:\.tar)?\.freeace$|\.7z$/i, ".exe")
    : path.replace(/\.exe$/i, archiveSuffix);
}

/** Output path the run will use; autofill may still hold the archive name. */
export function resolveSfxOutputPath(outputPath: string): string {
  return isSfxRequested()
    ? withSfxExtension(outputPath, true, currentFormat())
    : outputPath;
}

function rewriteOutputPaths(sfx: boolean): void {
  for (const id of OUTPUT_PATH_IDS) {
    const field = input(id);
    if (!field?.value) continue;
    const next = withSfxExtension(field.value, sfx, currentFormat());
    if (next === field.value) continue;
    if (state.lastAutoOutputPath === field.value) {
      state.lastAutoOutputPath = next;
    }
    field.value = next;
  }
}

/** Write the `.exe` name back before the run captures its output identity. */
export function applySfxOutputPath(): void {
  if (isSfxRequested()) rewriteOutputPaths(true);
}

/** Enable the toggles only where SFX output is possible; clear them elsewhere. */
export function syncSfxAvailability(format: string): void {
  const allowed = sfxAllowed(format, input("update-mode")?.checked ?? false);
  const wasRequested = input("sfx")?.checked ?? false;
  for (const id of SFX_TOGGLE_IDS) {
    const toggle = input(id);
    if (!toggle) continue;
    toggle.disabled = !allowed;
    if (!allowed) toggle.checked = false;
  }
  const row = document.getElementById("basic-sfx-row");
  if (row) row.hidden = !sfxPlatformSupported();
  if (wasRequested && !allowed) rewriteOutputPaths(false);
}

export function setSfxRequested(checked: boolean): void {
  for (const id of SFX_TOGGLE_IDS) {
    const toggle = input(id);
    if (toggle && !toggle.disabled) toggle.checked = checked;
  }
  rewriteOutputPaths(isSfxRequested());
}

export function wireSfxToggles(): void {
  for (const id of SFX_TOGGLE_IDS) {
    input(id)?.addEventListener("change", (event) => {
      setSfxRequested((event.target as HTMLInputElement).checked);
    });
  }
  input("update-mode")?.addEventListener("change", () => {
    syncSfxAvailability(currentFormat());
  });
}
