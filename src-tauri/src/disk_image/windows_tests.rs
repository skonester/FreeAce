use super::*;

const SECTOR: usize = 2048;

fn dual16(out: &mut [u8], value: u16) {
    out[..2].copy_from_slice(&value.to_le_bytes());
    out[2..4].copy_from_slice(&value.to_be_bytes());
}

fn dual32(out: &mut [u8], value: u32) {
    out[..4].copy_from_slice(&value.to_le_bytes());
    out[4..8].copy_from_slice(&value.to_be_bytes());
}

fn directory_record(out: &mut [u8], name: &[u8], block: u32, size: u32, is_dir: bool) -> usize {
    let len = 33 + name.len() + usize::from(name.len().is_multiple_of(2));
    out[0] = len as u8;
    dual32(&mut out[2..10], block);
    dual32(&mut out[10..18], size);
    out[18..25].copy_from_slice(&[126, 1, 1, 0, 0, 0, 0]);
    out[25] = if is_dir { 2 } else { 0 };
    dual16(&mut out[28..32], 1);
    out[32] = name.len() as u8;
    out[33..33 + name.len()].copy_from_slice(name);
    len
}

fn iso() -> Vec<u8> {
    // Minimal ISO 9660 with both path tables, a root directory and one file.
    let mut iso = vec![0; 24 * SECTOR];
    let pvd = &mut iso[16 * SECTOR..17 * SECTOR];
    pvd[..7].copy_from_slice(b"\x01CD001\x01");
    pvd[8..72].fill(b' ');
    pvd[40..47].copy_from_slice(b"FREEACE");
    dual32(&mut pvd[80..88], 24);
    dual16(&mut pvd[120..124], 1);
    dual16(&mut pvd[124..128], 1);
    dual16(&mut pvd[128..132], SECTOR as u16);
    dual32(&mut pvd[132..140], 10);
    pvd[140..144].copy_from_slice(&18u32.to_le_bytes());
    pvd[148..152].copy_from_slice(&19u32.to_be_bytes());
    directory_record(&mut pvd[156..190], &[0], 20, SECTOR as u32, true);
    pvd[190..813].fill(b' ');
    for offset in [813, 830, 847, 864] {
        pvd[offset..offset + 16].copy_from_slice(b"2026010100000000");
    }
    pvd[881] = 1;
    iso[17 * SECTOR..17 * SECTOR + 7].copy_from_slice(b"\xFFCD001\x01");
    iso[18 * SECTOR..18 * SECTOR + 10].copy_from_slice(&[1, 0, 20, 0, 0, 0, 1, 0, 0, 0]);
    iso[19 * SECTOR..19 * SECTOR + 10].copy_from_slice(&[1, 0, 0, 0, 0, 20, 0, 1, 0, 0]);
    let root = &mut iso[20 * SECTOR..21 * SECTOR];
    let dot = directory_record(root, &[0], 20, SECTOR as u32, true);
    let parent = directory_record(&mut root[dot..], &[1], 20, SECTOR as u32, true);
    directory_record(&mut root[dot + parent..], b"HELLO.TXT;1", 21, 7, false);
    iso[21 * SECTOR..21 * SECTOR + 7].copy_from_slice(b"FreeAce");
    iso
}

#[test]
fn corrupt_and_missing_images_report_distinct_failures() {
    let temp = tempfile::tempdir().unwrap();
    let path = temp.path().join("disc.iso");
    assert_eq!(
        validate_iso(&path).unwrap_err().kind(),
        io::ErrorKind::NotFound
    );
    std::fs::write(&path, b"invalid image").unwrap();
    assert_eq!(
        validate_iso(&path).unwrap_err().kind(),
        io::ErrorKind::InvalidData
    );
    assert!(attach(&temp.path().join("missing.iso")).is_err());
    std::fs::write(&path, iso()).unwrap();
    validate_iso(&path).unwrap();
    use std::os::windows::fs::OpenOptionsExt;
    let _lock = std::fs::OpenOptions::new()
        .read(true)
        .share_mode(0)
        .open(&path)
        .unwrap();
    assert_eq!(attach(&path).unwrap_err().raw_os_error(), Some(32));
}

#[test]
#[ignore = "requires an interactive Windows host with Virtual Disk access and a free drive letter"]
fn native_mount_reports_drive_and_detaches_before_temp_cleanup() {
    use std::io::Write;

    let parent = tempfile::tempdir().unwrap();
    let archive_root = tempfile::tempdir().unwrap();
    let archive = archive_root.path().join("image.zip");
    let mut zip = zip::ZipWriter::new(File::create(&archive).unwrap());
    zip.start_file("image.iso", zip::write::SimpleFileOptions::default())
        .unwrap();
    zip.write_all(&iso()).unwrap();
    zip.finish().unwrap();
    let temp = conversion_workspace(parent.path()).unwrap();
    let path = temp.directory.path().join("image.iso");
    let source = std::fs::read(&archive).unwrap();
    let job = hatchzip_core::engine::Job::open(&archive, false, None, Default::default()).unwrap();
    let journal = hatchzip_core::journal::Paths::for_archive(&archive_root.path().join("scratch"));
    let paused = crate::in_place::extract_verified_for_test(
        job,
        journal,
        temp.directory.path(),
        &AtomicBool::new(false),
        |_| {},
    )
    .unwrap();
    assert!(!paused);
    assert_eq!(std::fs::read(&archive).unwrap(), source);
    assert_eq!(
        super::super::extracted_image(temp.directory.path()).unwrap(),
        Path::new("image.iso")
    );
    validate_iso(&path).unwrap();
    let handle = attach(&path).expect("Windows Virtual Disk failed to attach the generated ISO");
    let mut mount = Mount {
        handle: Some(handle),
        temp: Some(temp),
        image: path.clone(),
        device: None,
        drive: None,
        ready_seen: false,
    };
    for _ in 0..50 {
        assert!(mount.refresh());
        if mount.ready_seen {
            break;
        }
        std::thread::sleep(Duration::from_millis(100));
    }
    let root = mount
        .info(1)
        .mount_point
        .expect("Windows assigned no drive letter");
    assert_eq!(
        std::fs::read(Path::new(&root).join("HELLO.TXT")).unwrap(),
        b"FreeAce"
    );
    mount.cleanup().unwrap();
    assert!(!path.exists());
    assert_eq!(std::fs::read_dir(parent.path()).unwrap().count(), 0);
}
