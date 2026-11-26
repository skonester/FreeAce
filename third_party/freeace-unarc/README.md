# FreeArc decoder for FreeAce

The Windows build ships this read-only helper as a separate executable. The
source is a subset of [go-unarc at `2236294`](https://github.com/n0madic/go-unarc/tree/2236294cc234701a7fb38ddd82f3d26cb83d64a5),
with the original [GPL version 2 license](LICENSE).

FreeAce builds the helper with Go 1.26 or newer. Run `go test ./...` here to
verify archive listing, extraction and integrity independently of the UI.

The included decoders support `storing`, `lzma`, `rep`, `4x4`, and `tor` and
their supported combinations. The archive format uses IEEE CRC by default.
The helper also accepts the reflected CRC polynomial `0x8e092638` observed in
RUNE installer payloads. The footer descriptor selects the checksum and the
helper verifies the control blocks and decoded files with that same checksum.

The helper takes the archive directly. Its inputs are the archive, a private
output directory for extraction, and an optional line-delimited file of exact
member paths. Password codecs, installer scripts, external codec plugins, and
split streams are unsupported. FreeAce handles cancellation, output quotas,
and publishing files from the private output directory.
