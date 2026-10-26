//! End-to-end FreeAce SFX test against the real Gleipnir sidecar and stub.
//! Skips (passes) when `npm run prepare:freeace` has not produced them.
#![cfg(windows)]

use std::path::{Path, PathBuf};
use std::process::Command;

fn binaries() -> Option<(PathBuf, PathBuf)> {
    let dir = Path::new(env!("CARGO_MANIFEST_DIR")).join("binaries");
    let arch = std::env::consts::ARCH;
    let gleipnir = dir.join(format!("gleipnir-{arch}-pc-windows-msvc.exe"));
    let stub = dir.join("freeace.sfx");
    (gleipnir.is_file() && stub.is_file()).then_some((gleipnir, stub))
}

fn temp_dir() -> PathBuf {
    let dir = std::env::temp_dir().join(format!(
        "freeace-sfx-it-{}",
        std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap()
            .as_nanos()
    ));
    std::fs::create_dir_all(&dir).unwrap();
    dir
}

#[test]
fn sfx_stub_with_appended_archive_lists_and_self_extracts() {
    let Some((gleipnir, stub)) = binaries() else {
        return;
    };
    let dir = temp_dir();
    let input = dir.join("in");
    std::fs::create_dir_all(input.join("nested")).unwrap();
    std::fs::write(input.join("a.txt"), "alpha").unwrap();
    let noise: Vec<u8> = (0..200_000u32)
        .map(|i| (i.wrapping_mul(2654435761) >> 13) as u8)
        .collect();
    std::fs::write(input.join("nested").join("b.bin"), &noise).unwrap();

    let archive = dir.join("plain.freeace");
    let created = Command::new(&gleipnir)
        .args(["c", "-1", "-q"])
        .arg(&archive)
        .arg(&input)
        .output()
        .unwrap();
    assert!(
        created.status.success(),
        "{}",
        String::from_utf8_lossy(&created.stderr)
    );

    // Same layout run_freeace publishes: stub bytes, then the whole archive.
    let sfx = dir.join("setup.exe");
    let mut joined = std::fs::read(&stub).unwrap();
    joined.extend(std::fs::read(&archive).unwrap());
    std::fs::write(&sfx, joined).unwrap();

    let listed = Command::new(&gleipnir).arg("l").arg(&sfx).output().unwrap();
    assert!(
        listed.status.success(),
        "{}",
        String::from_utf8_lossy(&listed.stderr)
    );
    let listing = String::from_utf8_lossy(&listed.stdout);
    assert!(
        listing.contains("a.txt") && listing.contains("nested/b.bin"),
        "{listing}"
    );

    let out = dir.join("out");
    let ran = Command::new(&sfx)
        .arg("-y")
        .arg(format!("-o{}", out.display()))
        .output()
        .unwrap();
    assert!(
        ran.status.success(),
        "{}",
        String::from_utf8_lossy(&ran.stderr)
    );
    assert_eq!(std::fs::read_to_string(out.join("a.txt")).unwrap(), "alpha");
    assert_eq!(
        std::fs::read(out.join("nested").join("b.bin")).unwrap(),
        noise
    );

    // A stub without a payload must refuse before creating anything.
    let empty = dir.join("empty-out");
    let bare = Command::new(&stub)
        .arg("-y")
        .arg(format!("-o{}", empty.display()))
        .output()
        .unwrap();
    assert!(!bare.status.success());
    assert!(!empty.exists());
    let _ = std::fs::remove_dir_all(dir);
}
