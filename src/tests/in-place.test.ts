import { beforeEach, describe, expect, it, vi } from "vitest";
import { invoke } from "@tauri-apps/api/core";
import { confirm } from "@tauri-apps/plugin-dialog";
import { state } from "../state";
import {
  inPlaceReviewText,
  runInPlaceExtraction,
  type InPlaceReview,
} from "../archive/in-place";
import {
  syncBasicExtractToPower,
  syncPowerToBasicExtract,
} from "../basic/sync";

const plan: InPlaceReview = {
  token: "review-token",
  free: 10e9,
  needed: 1e9,
  normalNeeds: 8e9,
  keepFree: 256 * 1024 * 1024,
  fits: true,
  reclaims: true,
  resuming: false,
  archiveKeptWhole: false,
};

beforeEach(() => {
  vi.clearAllMocks();
  state.inputs = ["/tmp/large.7z.001"];
  state.cancelRequested = false;
  for (const id of [
    "extract-path",
    "extract-password",
    "extract-extra-args",
    "extract-in-place",
    "basic-extract-in-place",
  ]) {
    if (!document.getElementById(id)) {
      const input = document.createElement("input");
      input.id = id;
      document.body.appendChild(input);
    }
  }
  (document.getElementById("extract-path") as HTMLInputElement).value =
    "/tmp/output";
  (document.getElementById("extract-extra-args") as HTMLInputElement).value =
    "";
  vi.mocked(invoke).mockImplementation(async (command) => {
    if (command === "plan_in_place_extract") return plan;
    if (command === "run_in_place_extract") return { paused: false };
    return undefined;
  });
});

describe("in-place extraction", () => {
  it("reviews space and archive consumption before using the single-use token", async () => {
    vi.mocked(confirm).mockResolvedValue(true);
    expect(await runInPlaceExtraction()).toBe("done");
    expect(confirm).toHaveBeenCalledWith(
      expect.stringContaining("archive is deleted"),
      expect.anything(),
    );
    expect(invoke).toHaveBeenCalledWith("run_in_place_extract", {
      token: plan.token,
    });
    expect(invoke).toHaveBeenLastCalledWith("discard_in_place_plan", {
      token: plan.token,
    });
  });

  it("discards a declined review without starting extraction", async () => {
    vi.mocked(confirm).mockResolvedValue(false);
    expect(await runInPlaceExtraction()).toBe("cancelled");
    expect(
      vi
        .mocked(invoke)
        .mock.calls.some(([command]) => command === "run_in_place_extract"),
    ).toBe(false);
    expect(invoke).toHaveBeenLastCalledWith("discard_in_place_plan", {
      token: plan.token,
    });
  });

  it("refuses insufficient space before confirmation", async () => {
    vi.mocked(invoke).mockResolvedValueOnce({ ...plan, fits: false });
    await expect(runInPlaceExtraction()).rejects.toThrow("free");
    expect(confirm).not.toHaveBeenCalled();
    expect(invoke).toHaveBeenLastCalledWith("discard_in_place_plan", {
      token: plan.token,
    });
  });

  it("reports a safely paused run", async () => {
    vi.mocked(confirm).mockResolvedValue(true);
    vi.mocked(invoke)
      .mockResolvedValueOnce(plan)
      .mockResolvedValueOnce({ paused: true });
    expect(await runInPlaceExtraction()).toBe("paused");
  });

  it("explains when archive reclamation cannot help the destination", () => {
    expect(inPlaceReviewText({ ...plan, reclaims: false })).toContain(
      "another filesystem",
    );
    expect(inPlaceReviewText({ ...plan, archiveKeptWhole: true })).toContain(
      "until extraction finishes",
    );
  });

  it("synchronizes the opt-in between Basic and Power", () => {
    const basic = document.getElementById(
      "basic-extract-in-place",
    ) as HTMLInputElement;
    const power = document.getElementById(
      "extract-in-place",
    ) as HTMLInputElement;
    basic.checked = true;
    syncBasicExtractToPower();
    expect(power.checked).toBe(true);
    power.checked = false;
    syncPowerToBasicExtract();
    expect(basic.checked).toBe(false);
  });
});
