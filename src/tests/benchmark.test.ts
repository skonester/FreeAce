import { describe, it, expect } from "vitest";
import { parseBenchmarkSummary } from "../main";
import { parseBenchmarkOutput, formatBenchmarkSummary } from "../benchmark";

// Tail of a real `7z b -mmt4 -md22` run (7-Zip 24.x, Ryzen 7 5700).
const REAL_OUTPUT = [
  "RAM size:   32632 MB,  # CPU hardware threads:  16",
  "RAM usage:    130 MB,  # Benchmark threads:      4",
  "",
  "                       Compressing  |                  Decompressing",
  "Dict     Speed Usage    R/U Rating  |      Speed Usage    R/U Rating",
  "         KiB/s     %   MIPS   MIPS  |      KiB/s     %   MIPS   MIPS",
  "",
  "22:      31114   401   7544  30268  |     389312   398   8338  33213",
  "----------------------------------  | ------------------------------",
  "Avr:     31114   401   7544  30268  |     389312   398   8338  33213",
  "Tot:             400   7941  31741",
].join("\n");

describe("parseBenchmarkOutput", () => {
  it("extracts rating, usage, throughput and thread counts", () => {
    expect(parseBenchmarkOutput(REAL_OUTPUT)).toEqual({
      rating: 31741,
      usagePercent: 400,
      compressKiBps: 31114,
      decompressKiBps: 389312,
      benchmarkThreads: 4,
      hardwareThreads: 16,
    });
  });

  it("degrades to nulls when only the Tot line is present", () => {
    expect(parseBenchmarkOutput("Tot:   1   2   3333\r\n")).toEqual({
      rating: 3333,
      usagePercent: 1,
      compressKiBps: null,
      decompressKiBps: null,
      benchmarkThreads: null,
      hardwareThreads: null,
    });
  });

  it("returns null when there is no Tot line", () => {
    expect(parseBenchmarkOutput("Everything is Ok")).toBeNull();
  });
});

describe("formatBenchmarkSummary", () => {
  it("explains the score in plain language", () => {
    const summary = parseBenchmarkOutput(REAL_OUTPUT)!;
    expect(formatBenchmarkSummary(summary)).toBe(
      "Score: 31,741 MIPS (higher is faster). " +
        "Compress 31.9 MB/s, decompress 399 MB/s. " +
        "Used 4 of 16 threads (~4 cores kept busy).",
    );
  });

  it("omits sections whose numbers are missing", () => {
    expect(
      formatBenchmarkSummary({
        rating: 4150,
        usagePercent: null,
        compressKiBps: null,
        decompressKiBps: null,
        benchmarkThreads: null,
        hardwareThreads: null,
      }),
    ).toBe("Score: 4,150 MIPS (higher is faster).");
  });

  it("uses singular 'thread' for a single-thread run", () => {
    expect(
      formatBenchmarkSummary({
        rating: 4150,
        usagePercent: 100,
        compressKiBps: null,
        decompressKiBps: null,
        benchmarkThreads: 1,
        hardwareThreads: null,
      }),
    ).toBe(
      "Score: 4,150 MIPS (higher is faster). Used 1 thread (~1 cores kept busy).",
    );
  });
});

describe("parseBenchmarkSummary", () => {
  it("composes parse and format", () => {
    expect(parseBenchmarkSummary(REAL_OUTPUT)).toContain("Score: 31,741 MIPS");
  });

  it("returns null when there is no Tot line", () => {
    expect(parseBenchmarkSummary("Everything is Ok")).toBeNull();
  });

  it("handles CRLF output", () => {
    expect(parseBenchmarkSummary("Tot:   1   2   3333\r\n")).toBe(
      "Score: 3,333 MIPS (higher is faster).",
    );
  });
});
