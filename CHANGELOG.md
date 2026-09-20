# ⬇️ Downloads

| Windows | macOS | Linux |
| :------ | :---- | :---- |
| **EXE: [x64](https://github.com/skonester/FreeAce/releases/download/v0.6.2/FreeAce-Windows-x64.exe) / [arm64](https://github.com/skonester/FreeAce/releases/download/v0.6.2/FreeAce-Windows-arm64.exe)** | **[Universal DMG](https://github.com/skonester/FreeAce/releases/download/v0.6.2/FreeAce-macOS.dmg)** | **AppImage:** [x64](https://github.com/skonester/FreeAce/releases/download/v0.6.2/FreeAce-Linux-x64.AppImage) |
| | **[Universal ZIP](https://github.com/skonester/FreeAce/releases/download/v0.6.2/FreeAce-macOS.zip)** | **DEB:** [x64](https://github.com/skonester/FreeAce/releases/download/v0.6.2/FreeAce-Linux-x64.deb) |
| | | **RPM:** [x64](https://github.com/skonester/FreeAce/releases/download/v0.6.2/FreeAce-Linux-x64.rpm) |
| | | **Flatpak:** [x64](https://github.com/skonester/FreeAce/releases/download/v0.6.2/FreeAce-Linux-x64.flatpak) |

> macOS downloads require macOS 26 or later.

> [!IMPORTANT]
> The `.sig` files attached to a release are NOT normal GPG signatures; they are for Tauri v2's updater to verify the integrity of updates before downloading and installing.
> ⚠️ Linux ARM64 AppImage/DEB/RPM are published only when that release is built for ARM64; Flatpak stays x64.

FreeAce! A cross platform 7Z gui frontend built on Tauri V2!

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
