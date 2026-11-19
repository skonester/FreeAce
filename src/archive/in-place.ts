import { invoke } from "@tauri-apps/api/core";
import { confirm } from "@tauri-apps/plugin-dialog";
import { state } from "../state";
import { $, formatSize } from "../utils";
import { setProgress, setStatus } from "../ui";
import { boundedJson } from "./backend-ipc";
import { setSevenZipRunInFlight, withLiveProgress } from "./runtime";
import { reportMountedImage, type DiskImageMount } from "../disk-images";

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

export function inPlaceReviewText(
  plan: InPlaceReview,
  keepArchive = false,
  mountImage = false,
): string {
  const lines = [
    plan.resuming
      ? "Resume this in-place extraction?"
      : keepArchive
        ? "Extract and keep this archive?"
        : "Extract and consume this archive?",
    `Free space: ${formatSize(plan.free)}`,
    `Space needed now: ${formatSize(plan.needed)} + ${formatSize(plan.keepFree)} kept free`,
  ];
  if (plan.resuming) {
    if (!plan.reclaims)
      lines.push(
        "The destination is on another filesystem. Reclaiming archive space will not free space for the output.",
      );
    if (plan.archiveKeptWhole)
      lines.push(
        "This filesystem retains the compressed stream until extraction finishes.",
      );
    lines.push(
      "If interrupted, keep the output, .hatchzip journal and .hatchzip-tail file. Select the first volume and the same destination to resume.",
    );
  } else if (!keepArchive) {
    lines.push(
      "The archive is deleted after FreeAce verifies and publishes the extraction.",
    );
  }
  if (mountImage)
    lines.push(
      "FreeAce will mount the extracted disk image and report its drive letter.",
    );
  return lines.join("\n\n");
}

export async function runInPlaceExtraction(
  keepArchive = false,
  mountImage = false,
): Promise<"done" | "paused" | "cancelled"> {
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
      keepArchive,
      mountImage,
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
        `This extraction needs ${formatSize(plan.needed + plan.keepFree)} free. ${formatSize(plan.free)} is available.`,
      );
    const accepted = await confirm(
      inPlaceReviewText(plan, keepArchive, mountImage),
      {
        title: "In-place extraction",
        kind: "warning",
        okLabel: plan.resuming
          ? "Resume extraction"
          : keepArchive
            ? "Extract"
            : "Extract and consume archive",
        cancelLabel: "Cancel",
      },
    );
    if (!accepted || state.cancelRequested) return "cancelled";
    setStatus("Extracting in place");
    return await withLiveProgress(async () => {
      setSevenZipRunInFlight(true);
      try {
        const result = await invoke<{
          paused: boolean;
          mounted?: DiskImageMount | null;
        }>("run_in_place_extract", { token: plan.token });
        if (result.mounted) reportMountedImage(result.mounted);
        return result.paused
          ? plan.resuming
            ? "paused"
            : "cancelled"
          : "done";
      } catch (error) {
        if (
          typeof error === "object" &&
          error !== null &&
          "message" in error &&
          typeof error.message === "string"
        ) {
          throw new Error(error.message);
        }
        throw error;
      } finally {
        setSevenZipRunInFlight(false);
      }
    });
  } finally {
    await invoke("discard_in_place_plan", { token: plan.token });
  }
}
