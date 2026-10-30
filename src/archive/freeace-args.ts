import { $ } from "../utils";
import { isSfxRequested, resolveSfxOutputPath, SFX_SWITCH } from "./sfx";
import { state } from "../state";
import {
  buildFreeaceExtractArgs,
  validateFreeaceOutputExtension,
} from "./freeace-format";

export {
  buildFreeaceExtractArgs,
  isFreeaceArchive,
  isFreeaceOutputPath,
  validateFreeaceOutputExtension,
} from "./freeace-format";

/**
 * Gleipnir's CLI is a fixed grammar (`c|x|l|t [flags] archive [paths...]`,
 * no `--` separator support at all -- see `main()` in gleipnir.c) rather
 * than 7z's open-ended switch set, so this stays much smaller than
 * src/archive/args.ts. There is no update-in-place mode, no password
 * support, and no method/dict/word-size/solid controls to map.
 */

const FREEACE_PRESET_BY_LEVEL: Record<string, string> = {
  "0": "-1",
  "1": "-1",
  "3": "-3",
  "5": "-5",
  "7": "-7",
  "9": "-9",
};

function freeacePresetFlag(): string {
  const level = $<HTMLSelectElement>("level").value;
  return FREEACE_PRESET_BY_LEVEL[level] ?? "-3";
}

function freeaceThreadFlag(): string | null {
  const threads = $<HTMLInputElement>("threads").value.trim();
  if (!threads || !/^\d+$/.test(threads)) return null;
  return `-t${threads}`;
}

export function buildFreeaceCompressArgs(): string[] {
  const sfx = isSfxRequested();
  const outputPath = resolveSfxOutputPath(
    $<HTMLInputElement>("output-path").value,
  );
  if (!outputPath) {
    throw new Error("Choose an output archive path.");
  }
  const extensionError = sfx
    ? outputPath.toLocaleLowerCase("en-US").endsWith(".exe")
      ? null
      : "Self-extracting archive output must end in .exe."
    : validateFreeaceOutputExtension(outputPath);
  if (extensionError) throw new Error(extensionError);
  if (state.inputs.length === 0) {
    throw new Error("Add at least one input.");
  }

  const flags = [freeacePresetFlag()];
  const threadFlag = freeaceThreadFlag();
  if (threadFlag) flags.push(threadFlag);
  // Backend marker: Gleipnir writes a plain archive and run_freeace prepends
  // the bundled self-extracting stub.
  if (sfx) flags.push(SFX_SWITCH);

  return ["c", ...flags, outputPath, ...state.inputs];
}

export function buildFreeaceExtractArgsFor(
  archive: string,
  destinationOverride?: string,
  selectedPaths: string[] = [],
): string[] {
  const dest = destinationOverride ?? $<HTMLInputElement>("extract-path").value;
  if (!dest) throw new Error("Choose a destination folder.");
  return buildFreeaceExtractArgs(archive, dest, selectedPaths);
}

export function buildFreeaceListArgs(archive: string): string[] {
  return ["l", archive];
}

export function buildFreeaceTestArgs(archive: string): string[] {
  return ["t", archive];
}
