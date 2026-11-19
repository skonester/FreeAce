# In-place extraction and disk images

In Basic or Power mode, select one archive or a volume from a numbered split
set and choose a destination. Enable **Extract in place (consume archive)** to
remove the source after extraction. Leave it off to keep the source. The
review dialog estimates working space before any files change. Choose **Cancel**
for a dry run.

FreeAce stages output beside the destination using the vendored HatchZip core.
It validates the archive members as they decode, checks the output tree, and
publishes it into the selected directory. Existing directories can receive new
members. FreeAce restores stored modification times, Unix file modes, and
supported Windows ZIP and 7z attributes. Directories inherit destination
access controls. A conflicting file stops the operation. New jobs need space for the
archive and its output at the same time. Merging into an existing directory also
needs space for a publication copy, which inherits the destination permissions.
FreeAce keeps the source on extraction failure or cancellation and cleans the
staging files. The transaction journal supports recovery after an unexpected
shutdown.

Verified extraction supports ZIP, 7z, tar, tar.gz/tgz, FreeAce and FreeAce SFX,
including numbered split sets such as `backup.7z.001`. Select any existing
numbered volume. Every volume must be present. Native ZIP spanning (`.z01`),
RAR, and other formats continue to use ordinary extraction. Enter the archive
password in the existing field. A nested archive is extracted as a file and
can be opened separately. Quick extraction from Explorer and Finder uses the
ordinary extraction workflow.

If an older FreeAce version already partially consumed an archive, keep the
output, the `.hatchzip` journal, and the `.hatchzip-tail` file together. Select
the first volume and the original destination, then enable **Extract in place
(consume archive)** to resume. These older jobs continue reclaiming archive
space from their existing checkpoints. If FreeAce was killed while retiring an
already-verified split set, any volumes that were moved but still exist retain
their original names inside a `.freeace-consume-*` directory beside the source.

## Mount disk images on Windows

Open **Disk images** from the toolbar to mount a file, check its drive letter,
or unmount it. FreeAce also accepts `freeace.exe --mount-image "C:\images\disc.iso"`
from a terminal, including when FreeAce is already running. The drive belongs
to the running FreeAce instance and is detached when it exits. Windows can take
a moment to assign a letter. Keep the Disk images dialog open to see updates.

Enable **Mount the extracted disk image** to mount the single image in a
supported archive after extraction. Leave **Extract in place (consume archive)**
off to keep the archive, or enable it to retire the source after the mount
succeeds. An extraction with no image or multiple independent images stops
before publication. To choose from several images, extract with automatic
mounting off, then use Disk images. If mounting fails after verified publication,
FreeAce keeps the source archive and the published output.

FreeAce uses DIM's MIT-licensed Windows Virtual Disk API integration. ISO and
UDF optical images mount read-only. CUE/BIN, raw BIN/IMG/RAW, NRG, basic MDS/MDF
and CCD/IMG images can be converted to a temporary ISO. Conversion requires
additional cache space and uses the first supported data track. Audio tracks,
compressed images such as CHD, and non-optical disk formats such as VHD and VHDX
are outside this workflow. Windows must support mounting the image and be able
to read its backing file. Windows sparse ISO files can be rejected by the native
backend.
