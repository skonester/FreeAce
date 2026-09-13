import { describe, it, expect } from "vitest";
import { parseFreeaceListing } from "../archive/freeace-listing";

describe("parseFreeaceListing", () => {
  it("parses member rows, sizes, and mtimes from `gleipnir l` output", () => {
    const stdout = [
      "archive format v3, preset -5, 2 members, 4 segments",
      "         12345           4200  2.722  2026-09-03 14:05  " +
        "CK3 Mod Translator 1.0.1.exe",
      "           128             90  5.625  2026-09-01 09:30  readme.txt",
      "         12473           4290  2.751  total",
      "                              123  recovery records: 1 block, one per 8 segments (+2.9%)",
      "                            67890  archive file on disk",
    ].join("\n");

    const info = parseFreeaceListing(stdout);
    expect(info.type).toBe("FreeAce");
    expect(info.encrypted).toBe(false);
    expect(info.entries).toHaveLength(2);
    expect(info.entries[0]).toEqual({
      path: "CK3 Mod Translator 1.0.1.exe",
      size: 12345,
      packedSize: 4200,
      modified: "2026-09-03 14:05",
      isFolder: false,
    });
    expect(info.entries[1].path).toBe("readme.txt");
    expect(info.physicalSize).toBe(67890);
  });

  it("returns no entries for an empty archive", () => {
    const stdout = [
      "archive format v3, preset -5, 0 members, 0 segments",
      "             0              0  0.000  total",
      "                            -  no recovery records",
      "                              128  archive file on disk",
    ].join("\n");

    const info = parseFreeaceListing(stdout);
    expect(info.entries).toEqual([]);
  });
});
