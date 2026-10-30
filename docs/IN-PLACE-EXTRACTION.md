# In-place extraction

Enable **Extract in place (consume archive)** in Basic or Power mode, select
one archive (or one numbered volume) and a destination, then click Extract.
FreeAce presents the space estimate and asks before consuming any archive data.
The option is off by default and is not used by Explorer/Finder quick extraction.

ZIP, 7z, uncompressed tar, tar.gz/tgz and FreeAce (`.freeace`) are supported, including numbered
split sets such as `backup.7z.001` or `backup.zip.001`. Any surviving numbered
volume can locate the first volume. Native ZIP spanning (`.z01`, `.z02`) and
other formats use ordinary extraction. Passwords can be entered in the existing
password field. Extra 7-Zip arguments and multiple selected archives are refused
in this mode. Existing output files are never intentionally overwritten.

FreeAce uses the vendored [HatchZip core](../third_party/hatchzip-core/README.md).
Independent ZIP entries, 7z blocks and tar entries are extracted from the end.
Output is flushed, its journal is committed, and only then are consumed archive
bytes truncated or complete volumes deleted. Supported large ZIP/tar files and
tar.gz streams can also reclaim consumed ranges on filesystems supporting holes.
The source archive is deleted when extraction completes.

The disk does not generally need room for both a full archive and its output.
It still needs working room: the saved index, the next extraction unit or stream
checkpoint, filesystem allocation overhead, and a 256 MiB reserve. A single solid
7z block can require room for all its output before anything can be reclaimed.
Output on another filesystem gets no space benefit from shrinking the archive.
Use the measured plan for the selected archive; an output-only guarantee would
be misleading.

After cancellation, a crash, or a full disk, keep the extracted output and the
`<archive>.hatchzip` and `<archive>.hatchzip-tail` files. Do not open a consumed
archive with an ordinary extractor. Select its first volume and the original
destination in FreeAce, enable in-place extraction, and run Extract again.
FreeAce resumes from the durable checkpoint. The archive may no longer be valid
as a standalone archive until extraction finishes. Do not delete the recovery
files or move/modify the output while extraction is incomplete.

## FreeAce archives

FreeAce support lives in the vendored HatchZip core as its own format
(`third_party/hatchzip-core/src/archive/freeace.rs`); the on-disk format is
unchanged.

- **Units.** Each member is a unit starting at its first segment. A
  deduplicated member shares an earlier member's segments, so it joins that
  member's unit. Members with no data ride with the lowest unit, which starts
  at offset 0 and takes the header with it. Before anything is touched, the
  index is checked so that every unit's data lies below the next unit's
  start. An archive that does not satisfy this is refused, never guessed at.
- **Saved index.** The index and trailer are the saved tail. Once the archive
  has been truncated, Gleipnir reads them from a temporary copy (`x -I`),
  while the segments still present are read from the archive itself.
- **Decoding and verification.** Gleipnir streams one member at a time to
  stdout (`x -O`), using every core it can fit in memory. A member's output
  file is published only after Gleipnir exits cleanly, which means every
  segment checksum and the member's SHA-256 matched. A failed member stops
  the run before its unit is committed, so the archive is not consumed past
  that point.
- **Recovery records.** These are interleaved with the segments, so they are
  reclaimed along with them. A fresh run therefore runs a full checksum scrub
  (`gleipnir t`) first and refuses a damaged archive; repair it, or extract it
  normally. A resumed run cannot be scrubbed, but each member is still
  verified as it is decoded.
- **Self-extracting `.exe`.** A FreeAce SFX is recognised by its trailer and
  the checksummed header after the stub. Unit offsets are taken in the file,
  so the stub is reclaimed with the lowest unit, and the decoder is told where
  the archive starts (`x -B`) once the index has been cut off. A 7z SFX is
  refused; extract it normally.
- **Not supported:** archives whose header does not record the index
  position.

Decoding a FreeAce archive costs as much CPU as compressing it did, so
in-place extraction runs at the same speed as ordinary FreeAce extraction.
