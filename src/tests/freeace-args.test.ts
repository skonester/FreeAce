import { describe, it, expect, beforeEach, vi } from "vitest";
import { invoke } from "@tauri-apps/api/core";
import {
  buildFreeaceCompressArgs,
  buildFreeaceExtractArgsFor,
  buildFreeaceListArgs,
  buildFreeaceTestArgs,
} from "../archive/freeace-args";
import {
  isFreeaceArchive,
  isFreeaceOutputPath,
  validateFreeaceOutputExtension,
} from "../archive/freeace-format";
import { state } from "../state";

function setSelectValue(id: string, value: string) {
  const el = document.getElementById(id) as HTMLSelectElement;
  el.value = value;
}

function setInputValue(id: string, value: string) {
  const el = document.getElementById(id) as HTMLInputElement;
  el.value = value;
}

beforeEach(() => {
  state.inputs = [];
  setSelectValue("level", "5");
  setInputValue("output-path", "");
  setInputValue("threads", "");
  setInputValue("extract-path", "");
});

describe("isFreeaceOutputPath / validateFreeaceOutputExtension", () => {
  it("recognizes .freeace and .tar.freeace, case-insensitively", () => {
    expect(isFreeaceOutputPath("archive.freeace")).toBe(true);
    expect(isFreeaceOutputPath("ARCHIVE.FREEACE")).toBe(true);
    expect(isFreeaceOutputPath("archive.tar.freeace")).toBe(true);
    expect(isFreeaceOutputPath("archive.7z")).toBe(false);
    expect(isFreeaceOutputPath("archive.freeace.txt")).toBe(false);
  });

  it("returns an error only for a non-freeace extension", () => {
    expect(validateFreeaceOutputExtension("out.freeace")).toBeNull();
    expect(validateFreeaceOutputExtension("out.tar.freeace")).toBeNull();
    expect(validateFreeaceOutputExtension("out.7z")).toMatch(
      /\.freeace or \.tar\.freeace/,
    );
  });
});

describe("buildFreeaceCompressArgs", () => {
  it("builds a compress command with a mapped preset and inputs", () => {
    state.inputs = ["file1.txt", "file2.txt"];
    setInputValue("output-path", "archive.freeace");
    setSelectValue("level", "9");

    expect(buildFreeaceCompressArgs()).toEqual([
      "c",
      "-9",
      "archive.freeace",
      "file1.txt",
      "file2.txt",
    ]);
  });

  it("appends a thread flag only when threads is a plain positive integer", () => {
    state.inputs = ["file1.txt"];
    setInputValue("output-path", "archive.freeace");
    setInputValue("threads", "4");

    expect(buildFreeaceCompressArgs()).toEqual([
      "c",
      "-5",
      "-t4",
      "archive.freeace",
      "file1.txt",
    ]);
  });

  it("ignores a non-numeric threads value", () => {
    state.inputs = ["file1.txt"];
    setInputValue("output-path", "archive.freeace");
    setInputValue("threads", "auto");

    expect(buildFreeaceCompressArgs()).toEqual([
      "c",
      "-5",
      "archive.freeace",
      "file1.txt",
    ]);
  });

  it("rejects an output path without a .freeace/.tar.freeace extension", () => {
    state.inputs = ["file1.txt"];
    setInputValue("output-path", "archive.7z");

    expect(() => buildFreeaceCompressArgs()).toThrow(
      /\.freeace or \.tar\.freeace/,
    );
  });

  it("requires an output path and at least one input", () => {
    expect(() => buildFreeaceCompressArgs()).toThrow(
      "Choose an output archive path.",
    );

    setInputValue("output-path", "archive.freeace");
    expect(() => buildFreeaceCompressArgs()).toThrow("Add at least one input.");
  });
});

describe("buildFreeaceExtractArgsFor", () => {
  it("builds an extract command using the extract-path field by default", () => {
    setInputValue("extract-path", "/tmp/out");
    expect(buildFreeaceExtractArgsFor("archive.freeace")).toEqual([
      "x",
      "-t0",
      "archive.freeace",
      "/tmp/out",
    ]);
  });

  it("prefers an explicit destination override", () => {
    setInputValue("extract-path", "/tmp/out");
    expect(buildFreeaceExtractArgsFor("archive.freeace", "/tmp/other")).toEqual(
      ["x", "-t0", "archive.freeace", "/tmp/other"],
    );
  });

  it("requires a destination", () => {
    expect(() => buildFreeaceExtractArgsFor("archive.freeace")).toThrow(
      "Choose a destination folder.",
    );
  });

  it("appends selected member paths for selective extraction", () => {
    expect(
      buildFreeaceExtractArgsFor("archive.freeace", "/tmp/out", [
        "a.txt",
        "dir/b.txt",
      ]),
    ).toEqual([
      "x",
      "-t0",
      "archive.freeace",
      "/tmp/out",
      "a.txt",
      "dir/b.txt",
    ]);
  });
});

describe("buildFreeaceListArgs / buildFreeaceTestArgs", () => {
  it("builds a list command", () => {
    expect(buildFreeaceListArgs("archive.freeace")).toEqual([
      "l",
      "archive.freeace",
    ]);
  });

  it("builds a test command", () => {
    expect(buildFreeaceTestArgs("archive.freeace")).toEqual([
      "t",
      "archive.freeace",
    ]);
  });
});

describe("isFreeaceArchive", () => {
  const invokeMock = vi.mocked(invoke);

  it("trusts the .freeace extension without asking the backend", async () => {
    invokeMock.mockClear();
    expect(await isFreeaceArchive("C:\a\b.freeace")).toBe(true);
    expect(await isFreeaceArchive("C:\a\b.7z")).toBe(false);
    expect(invokeMock).not.toHaveBeenCalled();
  });

  it("asks the backend about an .exe, so 7z SFX still goes to 7-Zip", async () => {
    invokeMock.mockResolvedValueOnce(true);
    expect(await isFreeaceArchive("C:\a\DLC.exe")).toBe(true);
    expect(invokeMock).toHaveBeenLastCalledWith("detect_freeace_archive", {
      path: "C:\a\DLC.exe",
    });
    invokeMock.mockResolvedValueOnce(false);
    expect(await isFreeaceArchive("C:\a\setup.EXE")).toBe(false);
  });

  it("treats a failed probe as not FreeAce", async () => {
    invokeMock.mockRejectedValueOnce(new Error("gone"));
    expect(await isFreeaceArchive("C:\a\DLC.exe")).toBe(false);
  });
});
