import { invoke } from "@tauri-apps/api/core";

/**
 * Extension helpers with no dependency on `../state` (which does eager DOM
 * lookups at module load time for the main workspace document). Shared by
 * both the main workspace (src/archive/freeace-args.ts) and the standalone
 * extract window (src/extract-window.ts), which renders a different HTML
 * document without those elements.
 */
export function isFreeaceOutputPath(path: string): boolean {
  const lower = path.toLocaleLowerCase("en-US");
  return lower.endsWith(".freeace") || lower.endsWith(".tar.freeace");
}

/**
 * Whether an archive to read is FreeAce: by extension, or for an `.exe` by
 * content, since a FreeAce self-extracting archive must go to Gleipnir while
 * a 7z one goes to 7-Zip. Output paths still use `isFreeaceOutputPath`.
 */
export async function isFreeaceArchive(path: string): Promise<boolean> {
  if (isFreeaceOutputPath(path)) return true;
  if (!/\.exe$/i.test(path)) return false;
  try {
    return (await invoke<boolean>("detect_freeace_archive", { path })) === true;
  } catch {
    return false;
  }
}

/**
 * Gleipnir decodes one segment per worker, and every segment of a member is
 * independent, so `-t0` (all cores) is a pure speedup on the read side. The
 * sidecar clamps the count to what installed RAM can hold, since each worker
 * owns a full model (~1 GB at -9). Flags must precede the archive path.
 */
export function buildFreeaceExtractArgs(
  archive: string,
  destination: string,
  selectedPaths: string[] = [],
): string[] {
  return ["x", "-t0", archive, destination, ...selectedPaths];
}

export function validateFreeaceOutputExtension(
  outputPath: string,
): string | null {
  if (isFreeaceOutputPath(outputPath)) return null;
  return "Output filename must end in .freeace or .tar.freeace for FreeAce format.";
}
