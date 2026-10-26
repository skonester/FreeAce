import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { buildArgs } from "../archive";
import { buildFreeaceCompressArgs } from "../archive/freeace-args";
import {
  applySfxOutputPath,
  isSfxRequested,
  setSfxRequested,
  syncSfxAvailability,
  withSfxExtension,
} from "../archive/sfx";
import { state } from "../state";

function el<T extends HTMLElement>(id: string): T {
  return document.getElementById(id) as T;
}

function setFormat(format: string) {
  el<HTMLSelectElement>("format").value = format;
  syncSfxAvailability(format);
}

let basicOutput: HTMLInputElement;

beforeEach(() => {
  document.body.classList.add("platform-windows");
  basicOutput = document.createElement("input");
  basicOutput.id = "basic-output-path";
  document.body.appendChild(basicOutput);
  state.inputs = ["C:\\data\\photos"];
  state.lastAutoOutputPath = null;
  (document.getElementById("app") as HTMLElement).dataset.mode = "add";
  el<HTMLInputElement>("update-mode").checked = false;
  el<HTMLSelectElement>("split-size").value = "";
  el<HTMLInputElement>("extra-args").value = "";
  el<HTMLInputElement>("password").value = "";
  el<HTMLInputElement>("encrypt-headers").checked = false;
  el<HTMLInputElement>("delete-after").checked = false;
  el<HTMLInputElement>("output-path").value = "C:\\data\\photos.7z";
  setFormat("7z");
  setSfxRequested(false);
});

afterEach(() => {
  document.body.classList.remove("platform-windows");
  basicOutput.remove();
});

describe("withSfxExtension", () => {
  it("swaps only a trailing archive suffix and .exe", () => {
    expect(withSfxExtension("C:\\a\\b.7z", true, "7z")).toBe("C:\\a\\b.exe");
    expect(withSfxExtension("C:\\a\\b.EXE", false, "7z")).toBe("C:\\a\\b.7z");
    expect(withSfxExtension("C:\\a.7z\\b.zip", true, "7z")).toBe(
      "C:\\a.7z\\b.zip",
    );
    expect(withSfxExtension("C:\\a\\b.tar.freeace", true, "freeace")).toBe(
      "C:\\a\\b.exe",
    );
    expect(withSfxExtension("C:\\a\\b.exe", false, "freeace")).toBe(
      "C:\\a\\b.freeace",
    );
    expect(withSfxExtension("C:\\a\\b.exe", false, "zip")).toBe("C:\\a\\b.exe");
  });
});

describe("SFX availability", () => {
  it("is offered only for 7z and FreeAce creates on Windows", () => {
    expect(el<HTMLInputElement>("sfx").disabled).toBe(false);
    expect(el<HTMLElement>("basic-sfx-row").hidden).toBe(false);

    setFormat("freeace");
    expect(el<HTMLInputElement>("sfx").disabled).toBe(false);

    setFormat("zip");
    expect(el<HTMLInputElement>("sfx").disabled).toBe(true);
    expect(el<HTMLInputElement>("basic-sfx").disabled).toBe(true);

    setFormat("7z");
    el<HTMLInputElement>("update-mode").checked = true;
    syncSfxAvailability("7z");
    expect(el<HTMLInputElement>("sfx").disabled).toBe(true);
  });

  it("is hidden and never requested off Windows", () => {
    document.body.classList.remove("platform-windows");
    syncSfxAvailability("7z");
    el<HTMLInputElement>("sfx").checked = true;
    expect(el<HTMLElement>("basic-sfx-row").hidden).toBe(true);
    expect(isSfxRequested()).toBe(false);
  });

  it("mirrors both toggles and swaps the visible output extensions", () => {
    basicOutput.value = "C:\\data\\photos.7z";
    state.lastAutoOutputPath = "C:\\data\\photos.7z";

    setSfxRequested(true);
    expect(el<HTMLInputElement>("sfx").checked).toBe(true);
    expect(el<HTMLInputElement>("basic-sfx").checked).toBe(true);
    expect(el<HTMLInputElement>("output-path").value).toBe(
      "C:\\data\\photos.exe",
    );
    expect(basicOutput.value).toBe("C:\\data\\photos.exe");
    expect(state.lastAutoOutputPath).toBe("C:\\data\\photos.exe");

    setSfxRequested(false);
    expect(el<HTMLInputElement>("output-path").value).toBe(
      "C:\\data\\photos.7z",
    );
  });

  it("clears the toggle on a non-SFX format and leaves the name to autofill", () => {
    state.lastAutoOutputPath = "C:\\data\\photos.7z";
    setSfxRequested(true);

    setFormat("zip");
    expect(el<HTMLInputElement>("sfx").checked).toBe(false);
    // Still tracked as autofilled, so the zip autofill replaces it next.
    expect(state.lastAutoOutputPath).toBe(
      el<HTMLInputElement>("output-path").value,
    );
  });
});

describe("buildArgs with SFX", () => {
  it("emits only the bare backend-resolved marker and an .exe output", () => {
    setSfxRequested(true);
    // Autofill may still hold the .7z name; the run uses the .exe name.
    el<HTMLInputElement>("output-path").value = "C:\\data\\photos.7z";

    const args = buildArgs();
    expect(args[0]).toBe("a");
    expect(args).toContain("-sfx");
    expect(args.some((arg) => /^-sfx./i.test(arg))).toBe(false);
    expect(args[args.indexOf("--") - 1]).toBe("C:\\data\\photos.exe");
    expect(el<HTMLInputElement>("output-path").value).toBe(
      "C:\\data\\photos.7z",
    );

    applySfxOutputPath();
    expect(el<HTMLInputElement>("output-path").value).toBe(
      "C:\\data\\photos.exe",
    );
  });

  it("rejects split volumes and non-exe output", () => {
    setSfxRequested(true);
    el<HTMLSelectElement>("split-size").value = "100m";
    expect(() => buildArgs()).toThrow("cannot be split into volumes");

    el<HTMLSelectElement>("split-size").value = "";
    el<HTMLInputElement>("output-path").value = "C:\\data\\photos.bin";
    expect(() => buildArgs()).toThrow("must end in .exe");
  });

  it("leaves ordinary 7z output untouched when SFX is off", () => {
    const args = buildArgs();
    expect(args).not.toContain("-sfx");
    expect(args[args.indexOf("--") - 1]).toBe("C:\\data\\photos.7z");
  });
});

describe("buildFreeaceCompressArgs with SFX", () => {
  beforeEach(() => {
    setFormat("freeace");
    el<HTMLSelectElement>("level").value = "9";
    el<HTMLInputElement>("threads").value = "";
    el<HTMLInputElement>("output-path").value = "C:\\data\\photos.freeace";
  });

  it("adds the backend marker before the .exe output", () => {
    setSfxRequested(true);
    expect(el<HTMLInputElement>("output-path").value).toBe(
      "C:\\data\\photos.exe",
    );
    expect(buildFreeaceCompressArgs()).toEqual([
      "c",
      "-9",
      "-sfx",
      "C:\\data\\photos.exe",
      "C:\\data\\photos",
    ]);
  });

  it("keeps plain .freeace output when SFX is off", () => {
    expect(buildFreeaceCompressArgs()).toEqual([
      "c",
      "-9",
      "C:\\data\\photos.freeace",
      "C:\\data\\photos",
    ]);
  });
});
