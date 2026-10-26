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
