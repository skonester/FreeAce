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

export function validateFreeaceOutputExtension(
  outputPath: string,
): string | null {
  if (isFreeaceOutputPath(outputPath)) return null;
  return "Output filename must end in .freeace or .tar.freeace for FreeAce format.";
}
