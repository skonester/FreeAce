//! Fresh jobs use the protected HatchZip reader and FreeAce's publish journal.
//! Source retirement starts only after verification, publication and any mount.

use super::*;
use crate::{disk_image, path_safety, process::CleanupPlan};
use hatchzip_core::archive::{Item, Kind};

pub(super) fn execute(
    app: &tauri::AppHandle,
    pending: &Pending,
    job: engine::Job,
    original_paths: Paths,
    stop: &AtomicBool,
    progress: impl Fn(f64) + Sync + 'static,
) -> Result<ResultInfo, ExtractionError> {
    process::recover_interrupted_transaction(app)?;
    let request = &pending.request;
    let destination = &request.destination;
    let (free, needed, _) = protected_plan(&job, destination)?;
    if free < needed.saturating_add(KEEP_FREE)
        || job.remaining_size() > process::MAX_EXTRACTED_BYTES
    {
        return Err("The verified extraction exceeds the available space or output limit.".into());
    }
    let volumes = job
        .storage
        .record()
        .into_iter()
        .map(|(path, _)| path)
        .collect::<Vec<_>>();
    #[cfg(windows)]
    if !request.keep_archive {
        for part in &volumes {
            if fs::metadata(part)
                .map_err(|e| e.to_string())?
                .permissions()
                .readonly()
            {
                return Err(format!(
                    "Archive volume is read-only: {}. Clear the consume option to keep it.",
                    part.display()
                )
                .into());
            }
        }
    }
    let cache = app.path().app_cache_dir().map_err(|e| e.to_string())?;
    let stage = process::next_extract_stage_path(destination, Some(&cache))?;
    let plan = CleanupPlan {
        staged_extract: Some((stage.path.clone(), destination.clone())),
        staged_archive: None,
        expected_archive_family: Vec::new(),
        staged_input_archive: None,
        cache_dir: Some(cache.clone()),
        stage_identities: vec![(stage.path.clone(), stage.identity)],
        max_extract_bytes: Some(process::MAX_EXTRACTED_BYTES),
        min_free_bytes: Some(KEEP_FREE),
    };
    if let Err(error) = process::write_cleanup_journal(app, &plan) {
        return Err(ExtractionError::new(
            "stage",
            with_cleanup(
                error,
                process::recover_interrupted_transaction(app)
                    .and_then(|()| process::rollback_protected_extract(&plan)),
            ),
            false,
        ));
    }
    let prepared = (|| {
        let scratch = process::create_private_stage_dir(destination, "input", Some(&cache))?;
        let paths = Paths::for_archive(&scratch.path.join("archive"));
        let result = extract(job, paths, &stage.path, stop, progress);
        let cleanup = process::remove_directory_if_matches(&scratch.path, &scratch.identity)
            .and_then(|()| process::unregister_pending_stage(&cache, &scratch.path));
        let paused = combine(result, cleanup)?;
        if paused || stop.load(Ordering::Relaxed) {
            return Ok((true, None));
        }
        process::validate_staged_tree(&stage.path, process::MAX_EXTRACTED_BYTES)?;
        let image = if request.mount_image {
            Some(disk_image::extracted_image(&stage.path).map_err(|error| error.to_string())?)
        } else {
            None
        };
        recheck_sources(&volumes, &original_paths, &pending.stamps)?;
        Ok((false, image))
    })();
    let image = match prepared {
        Ok((false, image)) if !stop.load(Ordering::Relaxed) => image,
        outcome => {
            let cleanup = process::recover_interrupted_transaction(app)
                .and_then(|()| process::unregister_pending_stage(&cache, &stage.path));
            combine(outcome, cleanup)?;
            return Ok(ResultInfo {
                paused: true,
                mounted: None,
            });
        }
    };
    if let Err(error) = process::commit_protected_extract(app, &plan) {
        let mut failure = ExtractionError::new(
            "publish",
            with_cleanup(
                error,
                process::recover_interrupted_transaction(app)
                    .and_then(|()| process::unregister_pending_stage(&cache, &stage.path)),
            ),
            false,
        );
        failure.output_published = None;
        return Err(failure);
    }
    process::clear_cleanup_journal(app)
        .map_err(|error| ExtractionError::new("cleanup", error, true))?;
    eprintln!("Verified extraction published to {}", destination.display());
    if stop.load(Ordering::Relaxed) {
        return Err(ExtractionError::new(
            "cancelled",
            "Extraction completed. Cancellation kept the source archive.".into(),
            true,
        ));
    }
    let mounted = image
        .map(|relative| disk_image::mount(app, &destination.join(relative), stop))
        .transpose()
        .map_err(|error| {
            let mut failure = ExtractionError::new(
                "mount",
                format!("Extraction completed and the archive was kept. Mount failed: {error}"),
                true,
            );
            failure.mount_error = Some(error);
            failure
        })?;
    if !request.keep_archive {
        let consume = recheck_sources(&volumes, &original_paths, &pending.stamps)
            .and_then(|()| retire(&volumes, &pending.stamps, stop));
        if let Err(error) = consume {
            let cleanup = mounted.as_ref().map_or(Ok(()), |info| {
                disk_image::release(app, info).map_err(|e| e.to_string())
            });
            return Err(ExtractionError::new(
                "consume",
                with_cleanup(
                    format!("Extraction completed. Archive cleanup failed: {error}"),
                    cleanup,
                ),
                true,
            ));
        }
        eprintln!("Consumed {} verified archive volume(s)", volumes.len());
    }
    Ok(ResultInfo {
        paused: false,
        mounted,
    })
}

pub(crate) fn extract(
    job: engine::Job,
    paths: Paths,
    output: &Path,
    stop: &AtomicBool,
    progress: impl Fn(f64) + Sync + 'static,
) -> Result<bool, String> {
    let metadata = job
        .units
        .iter()
        .flat_map(|unit| &unit.items)
        .filter(|item| {
            matches!(item.kind, Kind::Dir)
                || (cfg!(windows)
                    && matches!(item.kind, Kind::File)
                    && item.windows_attributes.is_some())
        })
        .cloned()
        .collect::<Vec<_>>();
    let journal =
        session::begin(&job, &paths, output.to_owned(), true).map_err(|e| format!("{e:#}"))?;
    let paused = run_job(job, journal, paths, stop, progress)?;
    if !paused {
        restore_staged_metadata(output, &metadata)?;
    }
    Ok(paused)
}

fn restore_staged_metadata(output: &Path, items: &[Item]) -> Result<(), String> {
    for item in items {
        let path = output.join(&item.path);
        let metadata = fs::symlink_metadata(&path).map_err(|error| error.to_string())?;
        path_safety::reject_link_or_reparse(&path, &metadata)?;
        if let (Kind::Dir, Some(modified)) = (&item.kind, item.mtime) {
            process::set_extracted_directory_time(&path, modified)?;
        }
        #[cfg(windows)]
        if let Some(attributes) = item.windows_attributes {
            process::set_extracted_windows_attributes(&path, attributes)?;
        }
    }
    Ok(())
}

fn recheck_sources(volumes: &[PathBuf], paths: &Paths, expected: &[Stamp]) -> Result<(), String> {
    let primary = volumes
        .first()
        .ok_or("The archive has no readable volumes.")?;
    if expected.len() != volumes.len() + 2
        || hatchzip_core::archive::storage::volume_set(primary).map_err(|e| format!("{e:#}"))?
            != volumes
        || paths.journal.try_exists().map_err(|e| e.to_string())?
        || paths.tail.try_exists().map_err(|e| e.to_string())?
    {
        return Err("The archive volume set or recovery files changed during extraction.".into());
    }
    for (part, stamp) in volumes.iter().zip(expected) {
        plain_path(part)?;
        let input = path_safety::open_regular_file_nofollow(part)?;
        let identity = process::archive_member_stamp(part, &input)?;
        if stamp.path != *part || stamp.identity.as_ref() != Some(&identity) {
            return Err(format!(
                "Archive volume changed during extraction: {}",
                part.display()
            ));
        }
    }
    Ok(())
}

fn retire(volumes: &[PathBuf], stamps: &[Stamp], stop: &AtomicBool) -> Result<(), String> {
    let primary = volumes
        .first()
        .ok_or("The archive has no readable volumes.")?;
    let quarantine = process::create_private_stage_dir(primary, "consume", None)?;
    // Keep the original names so every staged volume can be recovered if
    // the process dies between moves. This directory is excluded from sweeps.
    let mut moved = Vec::new();
    let result = (|| {
        for (original, stamp) in volumes.iter().zip(stamps).rev() {
            if stop.load(Ordering::Relaxed) {
                return Err("Cancellation kept the source archive.".into());
            }
            let name = original
                .file_name()
                .ok_or("Archive volume has no file name.")?;
            let held = quarantine.path.join(name);
            process::rename_file_no_replace(original, &held)?;
            moved.push((original, held));
            let (_, held) = moved.last().ok_or("Missing staged archive volume.")?;
            let file = path_safety::open_regular_file_nofollow(held)?;
            if Some(process::archive_member_stamp(original, &file)?) != stamp.identity {
                return Err(format!(
                    "Archive volume changed before retirement: {}",
                    original.display()
                ));
            }
        }
        Ok(())
    })();
    if let Err(error) = result {
        let mut failures = Vec::new();
        for (original, held) in moved.into_iter().rev() {
            if let Err(rollback) = process::rename_file_no_replace(&held, original) {
                failures.push(rollback);
            }
        }
        if !failures.is_empty() {
            return Err(format!(
                "{error} Recovery volumes are in {}. {}",
                quarantine.path.display(),
                failures.join(" ")
            ));
        }
        return Err(with_cleanup(
            error,
            process::remove_directory_if_matches(&quarantine.path, &quarantine.identity),
        ));
    }
    let remove = (|| {
        for (_, held) in moved {
            let identity = process::path_identity(&held)?;
            process::remove_regular_file_if_matches(&held, &identity)?;
        }
        process::remove_directory_if_matches(&quarantine.path, &quarantine.identity)
    })();
    remove.map_err(|error| {
        format!(
            "Source volumes were retired. Check {} for any remaining copies. {error}",
            quarantine.path.display()
        )
    })
}

fn with_cleanup(error: String, cleanup: Result<(), String>) -> String {
    match cleanup {
        Ok(()) => error,
        Err(detail) => format!("{error} Cleanup also failed: {detail}"),
    }
}

fn combine<T>(result: Result<T, String>, cleanup: Result<(), String>) -> Result<T, String> {
    match result {
        Ok(value) => cleanup.map(|()| value),
        Err(error) => Err(with_cleanup(error, cleanup)),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    fn zip(path: &Path) {
        let mut archive = zip::ZipWriter::new(fs::File::create(path).unwrap());
        let options = zip::write::SimpleFileOptions::default()
            .compression_method(zip::CompressionMethod::Stored)
            .external_attributes((0o100640 << 16) | 2)
            .last_modified_time(zip::DateTime::from_date_and_time(2023, 7, 8, 9, 10, 12).unwrap());
        archive.add_directory("nested/", options).unwrap();
        archive.start_file("nested/outer.zip", options).unwrap();
        archive.write_all(b"inner archive stays as a file").unwrap();
        archive.start_file("nested/disc.iso", options).unwrap();
        archive.write_all(b"disc data").unwrap();
        archive.finish().unwrap();
    }

    fn protected_extract(
        root: &Path,
        source: &Path,
        output: &Path,
        stop: bool,
    ) -> Result<bool, String> {
        let request = Request {
            archive: source.to_owned(),
            destination: output.to_owned(),
            password: None,
            keep_archive: false,
            mount_image: false,
        };
        let (view, _) = open_request(&request, false, false)?;
        let State::Fresh(job, _) = view else {
            panic!("expected fresh archive")
        };
        let scratch = root.join("scratch");
        fs::create_dir(&scratch).unwrap();
        let paths = Paths::for_archive(&scratch.join("archive"));
        extract(job, paths, output, &AtomicBool::new(stop), |_| {})
    }

    fn stamps_for(volumes: &[PathBuf]) -> Vec<Stamp> {
        volumes
            .iter()
            .map(|volume| {
                let file = path_safety::open_regular_file_nofollow(volume).unwrap();
                Stamp {
                    path: volume.clone(),
                    identity: Some(process::archive_member_stamp(volume, &file).unwrap()),
                    content: None,
                }
            })
            .collect()
    }

    #[test]
    fn verified_output_can_select_an_image_before_the_archive_is_consumed() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path();
        let source = root.join("archive.zip");
        let stage = root.join("staged");
        let destination = root.join("out");
        zip(&source);
        let bytes = fs::read(&source).unwrap();
        let reviewed = stamps_for(std::slice::from_ref(&source));
        assert!(!protected_extract(root, &source, &stage, false).unwrap());
        assert_eq!(fs::read(&source).unwrap(), bytes);
        assert_eq!(
            disk_image::extracted_image(&stage).unwrap(),
            Path::new("nested/disc.iso")
        );
        let disk = stage.join("nested/disc.iso");
        let metadata = fs::metadata(&disk).unwrap();
        let minimum_mtime = std::time::UNIX_EPOCH + Duration::from_secs(1_688_780_000);
        assert!(metadata.modified().unwrap() >= minimum_mtime);
        assert!(
            fs::metadata(stage.join("nested"))
                .unwrap()
                .modified()
                .unwrap()
                >= minimum_mtime
        );
        #[cfg(windows)]
        {
            use std::os::windows::fs::MetadataExt;
            use windows_sys::Win32::Storage::FileSystem::FILE_ATTRIBUTE_HIDDEN;
            assert_ne!(metadata.file_attributes() & FILE_ATTRIBUTE_HIDDEN, 0);
        }
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            assert_eq!(metadata.permissions().mode() & 0o777, 0o640);
        }
        process::merge_staged_extract(&stage, &destination, process::MAX_EXTRACTED_BYTES).unwrap();
        assert_eq!(
            fs::read(destination.join("nested/outer.zip")).unwrap(),
            b"inner archive stays as a file"
        );
        assert_eq!(
            fs::read(destination.join("nested/disc.iso")).unwrap(),
            b"disc data"
        );
        retire(
            std::slice::from_ref(&source),
            &reviewed,
            &AtomicBool::new(false),
        )
        .unwrap();
        assert!(!source.exists());
        assert!(fs::read_dir(root).unwrap().all(|entry| !entry
            .unwrap()
            .file_name()
            .to_string_lossy()
            .starts_with(".freeace-consume-")));
    }

    #[test]
    fn corrupt_payload_and_missing_split_part_leave_original_volumes_intact() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path();
        let source = root.join("bad.zip");
        let stage = root.join("staged");
        zip(&source);
        let mut bytes = fs::read(&source).unwrap();
        let offset = bytes
            .windows(9)
            .position(|window| window == b"disc data")
            .unwrap();
        bytes[offset] ^= 0xff;
        fs::write(&source, &bytes).unwrap();
        assert!(protected_extract(root, &source, &stage, false).is_err());
        assert_eq!(fs::read(&source).unwrap(), bytes);
        assert!(!stage.join("nested/disc.iso.hatchzip-part").exists());

        let first = root.join("missing.zip.001");
        let last = root.join("missing.zip.003");
        fs::write(&first, &bytes[..50]).unwrap();
        fs::write(&last, &bytes[50..]).unwrap();
        assert!(
            protected_extract(root, &first, &root.join("missing-output"), false)
                .unwrap_err()
                .contains("gap")
        );
        assert_eq!(fs::read(&first).unwrap(), bytes[..50]);
        assert_eq!(fs::read(&last).unwrap(), bytes[50..]);
        assert!(protected_extract(root, &root.join("absent.zip"), &stage, false).is_err());
    }

    #[cfg(windows)]
    #[test]
    fn locked_split_volume_rolls_back_earlier_retirements() {
        use std::os::windows::fs::OpenOptionsExt;
        use windows_sys::Win32::Storage::FileSystem::FILE_SHARE_READ;
        let temp = tempfile::tempdir().unwrap();
        let volumes = [
            temp.path().join("data.zip.001"),
            temp.path().join("data.zip.002"),
        ];
        for (index, volume) in volumes.iter().enumerate() {
            fs::write(volume, [index as u8]).unwrap();
        }
        let reviewed = stamps_for(&volumes);
        let _lock = fs::OpenOptions::new()
            .read(true)
            .share_mode(FILE_SHARE_READ)
            .open(&volumes[0])
            .unwrap();
        assert!(retire(&volumes, &reviewed, &AtomicBool::new(false)).is_err());
        for (index, volume) in volumes.iter().enumerate() {
            assert_eq!(fs::read(volume).unwrap(), [index as u8]);
        }
        assert_eq!(fs::read_dir(temp.path()).unwrap().count(), 2);
    }
}
