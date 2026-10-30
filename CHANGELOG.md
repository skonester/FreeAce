
FreeAce! A cross platform 7Z gui frontend built on Tauri V2!

## Changes in `v0.6.4:`

### 2026-10-25 (10/25/26): Self-extracting archives, FreeAce in-place extraction, 0.6.4

#### Implemented

- Added **Self-extracting .exe** to Basic and Power compression on Windows, for the 7z and FreeAce formats. It is a per-archive choice and is never saved as a default. Turning it on renames the output to `.exe`; it is available only for new archives, not for updates or split volumes.
- 7z SFX uses 7-Zip's GUI module, `7z.sfx`, taken from the same official 26.03 installers as the bundled 7-Zip and pinned in `7z-checksums.json` and `7z-provenance.json`. The webview never names a module: 7-Zip resolves a relative `-sfx` module against its working directory first, so the backend accepts only a bare `-sfx` and substitutes the absolute path of the bundled module after checking its SHA-256.
- FreeAce SFX uses a new FreeAce extractor stub (`src-tauri/sfx/freeace-sfx.c`, built by `prepare:freeace`) with the whole archive appended. When run, it offers a folder named after the `.exe`, asks before replacing existing files, and extracts on all cores; `-y` and `-o<folder>` support scripted use. Gleipnir now finds an archive appended to an executable by its self-checksummed header, with no format change. Creation writes a plain archive beside the target, then joins stub and archive into a temporary file and renames it into place, so a failed or cancelled run leaves no partial `.exe`. FreeAce also opens these `.exe` files itself: browsing, ordinary extraction and in-place extraction recognise a FreeAce SFX by its content and route it to Gleipnir, while a 7z SFX `.exe` is browsed and extracted by 7-Zip.
- Added **Extract in place (consume archive)** for FreeAce archives. HatchZip core gained a FreeAce format: members are units starting at their first segment, deduplicated members share their original's unit, and the index is checked so no truncation can cut data another unit still needs. Gleipnir decodes each member to stdout from a saved copy of the index (`x -O -I`), and a file is published only after its segment checksums and SHA-256 verify. A fresh run first requires a clean integrity scrub, because recovery records are reclaimed with the segments they protect.
- FreeAce extraction now decodes segments on all cores instead of one, about 6x faster on a 300 MB `-9` archive. Gleipnir now caps decode threads to fit installed RAM, as it already did for compression; without the cap, all cores at `-9` needed about 13 GB.
- Releases built by the Windows CI workflow now include signed updater artifacts and `latest-windows-x86_64.json` / `latest-windows-aarch64.json` when the `TAURI_SIGNING_PRIVATE_KEY` and `TAURI_SIGNING_PRIVATE_KEY_PASSWORD` repository secrets are set. The manifests are checked with the existing updater-manifest validator before upload.

#### Fixed

- **Open folder** after a Basic extraction did nothing for FreeAce archives and in-place extractions, because their destinations were never registered as openable. Basic mode now also shows an error toast when a folder cannot be opened, instead of failing silently.
- In-app update checks failed with "Could not fetch a valid release JSON from the remote" because the CI-published releases carried no updater manifests. The endpoint configuration was already correct.
- **Browse archive** in Basic mode could not scroll and clipped long listings.
- Selecting individual files to extract from a FreeAce archive in the browse view was rejected by the backend.
- Basic's **Encrypt file names** option did not hide for formats without header encryption.

#### Not implemented and current limits

- Self-extracting archives are Windows-only. The FreeAce stub is a console window rather than a graphical dialog. A 7z SFX `.exe` can be browsed and extracted normally but not in place; in-place mode refuses it before touching it.
- Gleipnir opens files with narrow (ANSI) paths on Windows, so FreeAce archives, including SFX stubs, in folders whose names use characters outside the system code page cannot be opened. This predates this release.
- FreeAce in-place extraction does not support archives whose header does not record the index position, and a partly consumed FreeAce archive cannot be restored, only resumed. It decodes at the same speed as ordinary FreeAce extraction.
- The in-app updater keeps failing until the signing secrets are added and a release is published by the updated workflow.

#### Validation and remaining verification

- On Windows: 352 backend unit tests, 10 sidecar roundtrips (including a 7z SFX built with a decoy module in the working directory), 1 FreeAce SFX roundtrip, 1 updater-signature example test, and 17 HatchZip core tests passed. New FreeAce in-place tests cover shared and empty members, subprocess crashes after output and journal commits resuming byte for byte, a corrupt index refused untouched, a member failing verification consuming nothing and publishing no bad data, and the same extraction and crash/resume runs on a FreeAce SFX `.exe`. A 7z SFX is refused in place with the archive untouched, and both SFX kinds validate while an ordinary program does not.
- A 303 MB, 237-member `-9` FreeAce archive was extracted in place in 407 s; every member matched Gleipnir's SHA-256 manifest and the source was fully consumed.
- TypeScript checks, ESLint, comment lint, Prettier, Clippy with warnings denied, and rustfmt passed. Six frontend tests and seven script tests fail identically on the 0.6.3 tree and are unrelated to this release.
- Not yet verified in the running app: the SFX toggles, the Basic browse scrolling fix, and FreeAce in-place extraction through the review dialog.

## Changes in `v0.6.3:`

### 2026-10-24 (10/24/26): In-place extraction, 0.6.3

#### Implemented

- Added **Extract in place (consume archive)** to Basic and Power extraction. It is off by default. Ordinary extraction and Explorer/Finder quick extraction retain their existing behavior.
- Integrated the vendored HatchZip 0.1.3 Rust core for ZIP, 7z, uncompressed tar, tar.gz/tgz, and numbered split archives such as `.zip.001` and `.7z.001`. Selecting a numbered volume locates the first volume of its set. Existing password fields support encrypted ZIP and 7z archives.
- Added a space estimate and explicit archive-consumption confirmation. A single-use token binds execution to the reviewed paths and source/recovery file identities; the token expires after ten minutes.
- Added progress, cooperative cancellation, and resume after interruption. Output is flushed, the journal is committed, and only then are archive bytes reclaimed. Completed extraction deletes the source archive and consumed split volumes. Supported ZIP/tar files and tar.gz streams can reclaim ranges within files on filesystems that support hole punching.
- Reused the existing archive-operation slot and close/quit protection. Basic mode bypasses ordinary 7-Zip encryption probing for this mode so partially consumed archives can be reopened through their saved index.
- Added path, destination-collision, and extraction temporary-file checks. Hardened the imported engine to use unique temporary files for atomic journal writes and unique scratch directories for filesystem probes, preserving unrelated sibling files.
- Added byte-for-byte extraction and forced-crash recovery tests, plus frontend review/cancellation/synchronization tests. Bundled the engine's license texts and documented usage and format feasibility in [IN-PLACE-EXTRACTION.md](docs/IN-PLACE-EXTRACTION.md).

#### Not implemented and current limits

- **`.freeace` in-place extraction is not implemented.** In-place mode refuses it before modifying its source; ordinary `.freeace` extraction is unchanged. Gleipnir's independent segments make future support structurally feasible, but it still needs a segment decoding interface, durable writes at file offsets, segment checkpoints, and reopening through a saved index. Deduplicated members, parity recovery groups, and full-file hash verification must be handled before shared compressed data can be discarded. No FreeAce on-disk format change was made.
- This is not an unconditional output-only space guarantee. The saved index, next extraction unit or streaming checkpoint, filesystem overhead, and a 256 MiB reserve require working room. A large solid 7z block may need space for its entire output before reclamation. Output on another filesystem does not benefit from shrinking the source.
- In-place mode accepts one selected archive or volume set at a time. Multiple selected archives and extra 7-Zip arguments are refused. Automatic password retry was not added to this mode; use the existing password field.
- Native ZIP spanning (`.z01`/`.z02`), RAR, and bzip2/xz compressed streams are not supported by this mode. Ordinary extraction remains available for those formats. The optional HatchZip RAR/UnRAR feature is not enabled by FreeAce.
- No destructive quick-extract integration, dedicated restore UI, or release/package publication was added.

#### Validation and remaining verification

- On Windows, the full backend run passed: 336 unit tests, 9 sidecar roundtrips, and 1 updater-signature example test. All 16 HatchZip core tests also passed. The final focused frontend run passed 121 tests.
- Real fixtures were extracted and compared byte for byte, including encrypted ZIP/7z and numbered ZIP/7z split volumes. Recovery passed after subprocess crashes around output/journal commits and streaming flush/journal/hole-punch checkpoints for stored ZIP, deflated ZIP, and tar.
- Frontend production build, TypeScript checks, ESLint/comment lint, Rust checks, and Clippy with warnings denied passed.
- The full frontend suite was not completely green: six failures were observed in existing tests. Four `basic-recent` tests and one `main-bootstrap` test failed because the environment's `localStorage` lacked `clear`/`setItem`; one `release-script-safeguards` test failed because Windows denied symlink creation. These failures were not fixed by this work.
- Cargo license generation completed, including bundled texts for the new engine and its main dependencies, but reported 45 unresolved package license texts overall in the generated `public/licenses-cargo-unresolved.json`. Release license completeness remains to be checked; this was not a release build.
- Huge production archives, manual native GUI behavior, and macOS/Linux execution were not verified. No large-archive performance or disk-usage measurements were made.

#### Handoff for the next agent

- Backend commands and review/resume orchestration: `src-tauri/src/in_place.rs`. Frontend review flow: `src/archive/in-place.ts`; operation routing: `src/archive/ops.ts`; Basic synchronization and resume probing: `src/basic/sync.ts` and `src/basic/actions.ts`.
- Engine source and upstream licenses: `third_party/hatchzip-core/`. Preserve the durability order: **flush output, commit journal, reclaim archive bytes**. Destructive tests must use disposable, reproducible archives.
- Recovery requires the original output plus `<archive>.hatchzip` and `<archive>.hatchzip-tail`. Keep these files and the remaining source volumes together; resume using the first volume, original destination, and the in-place option. Do not route a partially consumed archive through ordinary extraction.
- Continue verification with `cargo test --locked --manifest-path src-tauri/Cargo.toml --all-targets`, `cargo test --locked --manifest-path src-tauri/Cargo.toml -p hatchzip-core`, and the frontend tests. Resolve the documented environment/license gaps and validate huge archives and other platforms before treating this feature as release-verified.

## Changes in `v0.6.2:`

- **License:** FreeAce is now GPL-3.0-or-later. Upstream code remains available under MPL-2.0; see `LICENSE.md`.
- **UI:** Settings › Compression › Defaults now offers the `.freeace` format and greys out the controls Gleipnir does not support (method, dictionary, word size, solid, file-name encryption).
- **UI:** Basic mode home screen shows a larger FreeAce mark and drops the duplicate "Drop a file here" text above the dropzone.
- **UI:** The CPU benchmark now explains its result (score, compress/decompress throughput, and threads kept busy) instead of a bare number.
- **UI:** About panel updated: "Not affiliated with WinAce", new tagline, and links point to https://github.com/skonester/freeace.
- **Icons:** Windows, macOS, and Linux app icons regenerated from the FreeAce artwork.

## ℹ️ Release Info

- **Not affiliated with WinAce.** FreeAce is an independent fork; the `.freeace` format is powered by [Gleipnir](https://github.com/ValisSowilo/Gleipnir).
- **Upstream:** This project is forked from an MPL-2.0 codebase. Its earlier release history is not carried here.
- **Windows installers:** Separate x64 and Arm64 installers are provided for their respective architectures.
