
FreeAce! A cross platform 7Z gui frontend built on Tauri V2!

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
