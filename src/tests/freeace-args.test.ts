import { describe, it, expect, beforeEach } from "vitest";
import {
  buildFreeaceCompressArgs,
  buildFreeaceExtractArgsFor,
} from "../archive/freeace-args";
import {
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
      "archive.freeace",
      "/tmp/out",
    ]);
  });

  it("prefers an explicit destination override", () => {
    setInputValue("extract-path", "/tmp/out");
    expect(buildFreeaceExtractArgsFor("archive.freeace", "/tmp/other")).toEqual(
      ["x", "archive.freeace", "/tmp/other"],
    );
  });

  it("requires a destination", () => {
    expect(() => buildFreeaceExtractArgsFor("archive.freeace")).toThrow(
      "Choose a destination folder.",
    );
  });
});
