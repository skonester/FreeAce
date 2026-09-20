/**
 * Which compression controls each output format actually honours. Shared by
 * the main create-archive form (src/presets.ts) and the Settings › Compression
 * › Defaults panel (src/settings.ts) so both grey out the same fields.
 *
 * `freeace` is driven by Gleipnir, whose CLI only takes a preset level
 * (-1..-9) and a thread count -- see src/archive/freeace-args.ts -- so every
 * 7z-specific knob is unsupported there.
 */

// PPMd/BZip2 need different memory/order controls than this form exposes.
// Offering them with generic dictionary/word-size fields generated invalid
// native commands, so keep only methods this UI can configure correctly.
const METHODS_BY_FORMAT: Record<string, string[]> = {
  "7z": ["lzma2", "lzma"],
  zip: ["deflate", "lzma"],
  tar: [],
  gzip: [],
  bzip2: [],
  xz: [],
  freeace: [],
};

export function supportedMethodsForFormat(format: string): string[] {
  return METHODS_BY_FORMAT[format] ?? [];
}

export function formatSupportsDictionary(
  format: string,
  method: string,
): boolean {
  return (
    format === "7z" ||
    format === "xz" ||
    (format === "zip" && method === "lzma")
  );
}

export function formatSupportsWordSize(format: string): boolean {
  return (
    format === "7z" || format === "xz" || format === "gzip" || format === "zip"
  );
}

// Solid mode is only supported for 7z archives.
export function formatSupportsSolid(format: string): boolean {
  return format === "7z";
}

// Stream formats have no store mode, and Gleipnir has no store/no-compression
// mode either -- its presets run -1 (fastest) through -9 (smallest) -- so
// "0 - Store" would silently compress anyway under a label that promised it
// wouldn't.
export function formatSupportsStoreLevel(format: string): boolean {
  return format === "7z" || format === "zip";
}
