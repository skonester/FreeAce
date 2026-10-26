# HatchZip core

Vendored from the user-supplied HatchZip 0.1.3 source tree,
https://github.com/xevrion/hatchzip (MIT OR Apache-2.0).
The upstream license texts are included here. RAR/UnRAR is not enabled.

Only the core library is included. Cargo workspace fields were expanded into
a standalone manifest. FreeAce uses unique temporary files for atomic journal
writes and unique scratch directories for filesystem probes, so existing
siblings cannot be overwritten or removed. The sources retain upstream's journal names so
interrupted extractions remain compatible with HatchZip.

FreeAce adds one format that upstream does not have: `src/archive/freeace.rs`
reads FreeAce (Gleipnir v2) archives and decodes their members through the
Gleipnir sidecar, which the front end registers with `set_decoder`. See
docs/IN-PLACE-EXTRACTION.md in the FreeAce repository.

The durability invariant is: flush output, commit the journal, then reclaim
archive bytes. Do not reorder these operations. Never test destructive
extraction against archives that cannot be recreated.
