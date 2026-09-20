import { invokeRun7z } from "./archive/backend-ipc";
import { $ } from "./utils";

export interface BenchmarkSummary {
  /** Combined compress/decompress rating from the trailing "Tot:" line, in MIPS. */
  rating: number;
  /** Average CPU usage across the run, in percent (400 = four cores busy). */
  usagePercent: number | null;
  /** Average compression throughput in KiB/s, from the "Avr:" line. */
  compressKiBps: number | null;
  /** Average decompression throughput in KiB/s, from the "Avr:" line. */
  decompressKiBps: number | null;
  /** "# Benchmark threads:" header value. */
  benchmarkThreads: number | null;
  /** "# CPU hardware threads:" header value. */
  hardwareThreads: number | null;
}

/**
 * Pull the useful numbers out of `7z b` output. The tail of the report is:
 *
 *                        Compressing  |                  Decompressing
 *   Dict     Speed Usage    R/U Rating  |      Speed Usage    R/U Rating
 *            KiB/s     %   MIPS   MIPS  |      KiB/s     %   MIPS   MIPS
 *   ...
 *   Avr:     31114   401   7544  30268  |     389312   398   8338  33213
 *   Tot:             400   7941  31741
 *
 * Only the "Tot:" rating is required; everything else degrades to null.
 */
export function parseBenchmarkOutput(stdout: string): BenchmarkSummary | null {
  const lines = stdout.split(/\r?\n/).map((line) => line.trim());

  let rating: number | null = null;
  let usagePercent: number | null = null;
  for (let i = lines.length - 1; i >= 0; i--) {
    if (!lines[i].startsWith("Tot:")) continue;
    const nums = lines[i].match(/\d+/g);
    if (nums && nums.length > 0) {
      rating = Number(nums[nums.length - 1]);
      if (nums.length >= 3) usagePercent = Number(nums[nums.length - 3]);
    }
    break;
  }
  if (rating === null) return null;

  let compressKiBps: number | null = null;
  let decompressKiBps: number | null = null;
  for (let i = lines.length - 1; i >= 0; i--) {
    if (!lines[i].startsWith("Avr:")) continue;
    const [compressSide, decompressSide] = lines[i].split("|");
    const compressNums = compressSide.match(/\d+/g);
    const decompressNums = decompressSide?.match(/\d+/g);
    if (compressNums && compressNums.length >= 4) {
      compressKiBps = Number(compressNums[0]);
    }
    if (decompressNums && decompressNums.length >= 4) {
      decompressKiBps = Number(decompressNums[0]);
    }
    break;
  }

  const readHeader = (pattern: RegExp): number | null => {
    for (const line of lines) {
      const match = line.match(pattern);
      if (match) return Number(match[1]);
    }
    return null;
  };

  return {
    rating,
    usagePercent,
    compressKiBps,
    decompressKiBps,
    benchmarkThreads: readHeader(/# Benchmark threads:\s*(\d+)/),
    hardwareThreads: readHeader(/# CPU hardware threads:\s*(\d+)/),
  };
}

function formatMBps(kibps: number): string {
  const mbps = (kibps * 1024) / 1_000_000;
  const digits = mbps >= 100 ? 0 : 1;
  return `${mbps.toLocaleString("en-US", { maximumFractionDigits: digits })} MB/s`;
}

/**
 * Turn the parsed numbers into a sentence a user can act on. The raw 7z
 * rating is meaningless on its own, so it is always qualified with "higher
 * is faster" and, where available, real throughput and thread usage.
 */
export function formatBenchmarkSummary(summary: BenchmarkSummary): string {
  const parts = [
    `Score: ${summary.rating.toLocaleString("en-US")} MIPS (higher is faster).`,
  ];

  const speeds: string[] = [];
  if (summary.compressKiBps !== null) {
    speeds.push(`compress ${formatMBps(summary.compressKiBps)}`);
  }
  if (summary.decompressKiBps !== null) {
    speeds.push(`decompress ${formatMBps(summary.decompressKiBps)}`);
  }
  if (speeds.length > 0) {
    const sentence = speeds.join(", ");
    parts.push(sentence.charAt(0).toUpperCase() + sentence.slice(1) + ".");
  }

  if (summary.benchmarkThreads !== null) {
    let threads = `Used ${summary.benchmarkThreads}`;
    if (summary.hardwareThreads !== null) {
      threads += ` of ${summary.hardwareThreads}`;
    }
    threads += summary.benchmarkThreads === 1 ? " thread" : " threads";
    if (summary.usagePercent !== null) {
      const busyCores = summary.usagePercent / 100;
      threads += ` (~${busyCores.toLocaleString("en-US", { maximumFractionDigits: 1 })} cores kept busy)`;
    }
    parts.push(threads + ".");
  }

  return parts.join(" ");
}

export function parseBenchmarkSummary(stdout: string): string | null {
  const summary = parseBenchmarkOutput(stdout);
  return summary ? formatBenchmarkSummary(summary) : null;
}

export async function runBenchmark() {
  const button = $("run-benchmark") as HTMLButtonElement;
  const result = $("benchmark-result");
  button.disabled = true;
  result.textContent = "Running benchmark… this takes a few seconds.";
  try {
    const res = await invokeRun7z<{ stdout: string; code: number }>({
      args: ["b"],
    });
    const summary = parseBenchmarkSummary(res.stdout);
    result.textContent = summary ?? "Benchmark finished (no rating reported).";
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    result.textContent = `Benchmark failed: ${msg}`;
  } finally {
    button.disabled = false;
  }
}
