import { $ } from "../utils";
import { state } from "../state";
import { validateFreeaceOutputExtension } from "./freeace-format";

export {
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
  const outputPath = $<HTMLInputElement>("output-path").value;
  if (!outputPath) {
    throw new Error("Choose an output archive path.");
  }
  const extensionError = validateFreeaceOutputExtension(outputPath);
  if (extensionError) throw new Error(extensionError);
  if (state.inputs.length === 0) {
    throw new Error("Add at least one input.");
  }

  const flags = [freeacePresetFlag()];
  const threadFlag = freeaceThreadFlag();
  if (threadFlag) flags.push(threadFlag);

  return ["c", ...flags, outputPath, ...state.inputs];
}

export function buildFreeaceExtractArgsFor(
  archive: string,
  destinationOverride?: string,
): string[] {
  const dest = destinationOverride ?? $<HTMLInputElement>("extract-path").value;
  if (!dest) throw new Error("Choose a destination folder.");
  return ["x", archive, dest];
}
