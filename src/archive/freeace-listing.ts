import type { ArchiveInfo, BrowseEntry } from "../browse-model";

/**
 * Parses `gleipnir l` output (see `do_list()` in third_party/gleipnir/gleipnir.c):
 * a fixed-width `%14llu %14llu %6.3f  <mtime>  <name>` line per member, a
 * matching `total` line, then disk/recovery-record footer lines. Gleipnir has
 * no directory members (it walks directories but only stores files) and no
 * password support, so `isFolder`/`encrypted` are always false.
 */
const MEMBER_LINE = /^\s*(\d+)\s+(\d+)\s+[\d.]+\s\s([\s\S]*)$/;
const DATE_PREFIX = /^(\d{4}-\d{2}-\d{2} \d{2}:\d{2})  ([\s\S]*)$/;

export function parseFreeaceListing(stdout: string): ArchiveInfo {
  const entries: BrowseEntry[] = [];

  for (const line of stdout.split(/\r?\n/)) {
    const match = line.match(MEMBER_LINE);
    if (!match) continue;
    const [, sizeStr, packedStr, rest] = match;
    if (rest.trim() === "total") continue;

    let modified = "";
    let name = rest;
    const dateMatch = rest.match(DATE_PREFIX);
    if (dateMatch) {
      modified = dateMatch[1];
      name = dateMatch[2];
    } else if (rest.startsWith("  ")) {
      name = rest.slice(2);
    }
    if (!name) continue;

    entries.push({
      path: name,
      size: parseInt(sizeStr, 10) || 0,
      packedSize: parseInt(packedStr, 10) || 0,
      modified,
      isFolder: false,
    });
  }

  const diskSizeMatch = stdout.match(/(\d+)\s+archive file on disk/);

  return {
    type: "FreeAce",
    physicalSize: diskSizeMatch ? parseInt(diskSizeMatch[1], 10) || 0 : 0,
    method: "Gleipnir",
    solid: true,
    encrypted: false,
    entries,
  };
}
