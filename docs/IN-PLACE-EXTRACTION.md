# In-place extraction

Enable **Extract in place (consume archive)** in Basic or Power mode, select
one archive (or one numbered volume) and a destination, then click Extract.
FreeAce presents the space estimate and asks before consuming any archive data.
The option is off by default and is not used by Explorer/Finder quick extraction.

ZIP, 7z, uncompressed tar and tar.gz/tgz are supported, including numbered
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

## FreeAce format feasibility

The Gleipnir v2 container behind `.freeace` is structurally suitable for future
support: segments are independently modelled and indexed by compressed offset,
length and checksums. The index and trailer live at the end and would need to
be saved before reclaiming archive bytes.

The current Gleipnir sidecar only offers whole-archive extraction. Integration
needs a segment decoding interface, durable writes to the correct file offsets,
a journal of completed segments, and a way to reopen a shortened container
using its saved index. Deduplicated members share segments; every referenced
output must be durable before a shared segment is reclaimed. Optional parity
groups also reference compressed segments, so recovery must finish before
discarding any segment it depends on. Full-file hashes must remain verifiable
across interruption and resume.

This is feasible without necessarily changing the on-disk format, but it is
not implemented in this version. `.freeace` continues to use ordinary extraction;
in-place mode refuses it before changing the source.
