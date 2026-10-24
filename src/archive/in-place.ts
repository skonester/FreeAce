import { invoke } from "@tauri-apps/api/core";
import { confirm } from "@tauri-apps/plugin-dialog";
import { state } from "../state";
import { $, formatSize } from "../utils";
import { setProgress, setStatus } from "../ui";
import { boundedJson } from "./backend-ipc";
import { setSevenZipRunInFlight, withLiveProgress } from "./runtime";

export interface InPlaceReview {
  token: string;
  free: number;
  needed: number;
  normalNeeds: number;
  keepFree: number;
  fits: boolean;
  reclaims: boolean;
  resuming: boolean;
  archiveKeptWhole: boolean;
}

export function inPlaceReviewText(plan: InPlaceReview): string {
  const lines = [
    plan.resuming
      ? "Resume this in-place extraction?"
      : "Extract and consume this archive?",
    `Free space: ${formatSize(plan.free)}`,
    `Space needed now: ${formatSize(plan.needed)} + ${formatSize(plan.keepFree)} kept free`,
    `Ordinary extraction needs: ${formatSize(plan.normalNeeds)}`,
  ];
  if (!plan.reclaims)
    lines.push(
      "The destination is on another filesystem. Shrinking the archive will not free space for the output.",
    );
  if (plan.archiveKeptWhole)
    lines.push(
      "This filesystem cannot reclaim this compressed stream until extraction finishes.",
    );
  lines.push(
    "Archive bytes and split volumes are removed as verified output is saved. The archive is deleted when extraction finishes.",
    "If interrupted, keep the output, .hatchzip journal and .hatchzip-tail file. Select the first volume and the same destination with this option enabled to resume.",
  );
  return lines.join("\n\n");
}

export async function runInPlaceExtraction(): Promise<
  "done" | "paused" | "cancelled"
> {
  if (state.inputs.length !== 1)
    throw new Error(
      "Select one archive or one volume of a split archive for in-place extraction.",
    );
  if ($<HTMLInputElement>("extract-extra-args").value.trim())
    throw new Error("Clear extra arguments before using in-place extraction.");
  const archive = state.inputs[0];
  const destination = $<HTMLInputElement>("extract-path").value.trim();
  if (!destination) throw new Error("Choose a destination folder.");
  const requestJson = boundedJson(
    {
      archive,
      destination,
      password: $<HTMLInputElement>("extract-password").value || undefined,
    },
    128 * 1024,
    "In-place extraction request",
  );
  setProgress("Reading archive and calculating space...");
  const plan = await invoke<InPlaceReview>("plan_in_place_extract", {
    requestJson,
  });
  try {
    if (state.cancelRequested) return "cancelled";
    if (!plan.fits)
      throw new Error(
        `This extraction needs ${formatSize(plan.needed + plan.keepFree)} free; ${formatSize(plan.free)} is available. Large solid 7z blocks may need room for the entire block before space can be reclaimed.`,
      );
    const accepted = await confirm(inPlaceReviewText(plan), {
      title: "In-place extraction",
      kind: "warning",
      okLabel: plan.resuming
        ? "Resume extraction"
        : "Extract and consume archive",
      cancelLabel: "Cancel",
    });
    if (!accepted || state.cancelRequested) return "cancelled";
    setStatus("Extracting in place");
    return await withLiveProgress(async () => {
      setSevenZipRunInFlight(true);
      try {
        const result = await invoke<{ paused: boolean }>(
          "run_in_place_extract",
          { token: plan.token },
        );
        return result.paused ? "paused" : "done";
      } finally {
        setSevenZipRunInFlight(false);
      }
    });
  } finally {
    await invoke("discard_in_place_plan", { token: plan.token });
  }
}
