//! Destructive extraction is separate from the ordinary staged 7-Zip path.
//! A single-use review token binds the run to the paths and file identities
//! inspected by the space planner. No archive bytes change during planning.

use std::{
    fs,
    path::{Component, Path, PathBuf},
    sync::{
        atomic::{AtomicBool, AtomicU64, Ordering},
        Arc, Mutex,
    },
    time::{Duration, Instant},
};

use hatchzip_core::{
    engine::{self, Outcome, Progress, Run},
    journal::{Journal, Paths},
    session::{self, State},
};
use serde::{Deserialize, Serialize};
use tauri::{Emitter, Manager};

use crate::process::{self, RunningProcess};

const KEEP_FREE: u64 = 256 * 1024 * 1024;

#[derive(Clone, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Request {
    archive: PathBuf,
    destination: PathBuf,
    password: Option<String>,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Review {
    token: String,
    free: u64,
    needed: u64,
    normal_needs: u64,
    keep_free: u64,
    fits: bool,
    reclaims: bool,
    resuming: bool,
    archive_kept_whole: bool,
}

#[derive(PartialEq, Eq)]
struct Stamp {
    path: PathBuf,
    identity: Option<String>,
    content: Option<[u8; 32]>,
}

struct Pending {
    token: String,
    owner: String,
    created: Instant,
    request: Request,
    stamps: Vec<Stamp>,
}

#[derive(Default)]
pub struct InPlacePlans(Mutex<Option<Pending>>);

/// Keep the existing operation slot and close/quit protection until the
/// blocking worker has stopped, including when the invoking window goes away.
struct Slot {
    app: tauri::AppHandle,
    stop: Arc<AtomicBool>,
}

impl Slot {
    fn claim(app: tauri::AppHandle, owner: &str) -> Result<Self, String> {
        let stop = Arc::new(AtomicBool::new(false));
        {
            let state = app.state::<RunningProcess>();
            let mut operation = process::lock_process(&state)?;
            process::ensure_idle_mut(&mut operation)?;
            operation.preparing = true;
            operation.owner_label = Some(owner.to_owned());
            operation.in_place_stop = Some(stop.clone());
        }
        Ok(Self { app, stop })
    }
}

impl Drop for Slot {
    fn drop(&mut self) {
        process::release_prepare_slot_best_effort(&self.app.state::<RunningProcess>());
    }
}

fn plain_path(path: &Path) -> Result<(), String> {
    if !path.is_absolute() || path.components().any(|c| matches!(c, Component::ParentDir)) {
        return Err("Choose an absolute path without parent-directory components.".into());
    }
    for ancestor in path.ancestors() {
        match fs::symlink_metadata(ancestor) {
            Ok(meta) => crate::path_safety::reject_link_or_reparse(ancestor, &meta)?,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {}
            Err(e) => return Err(e.to_string()),
        }
    }
    Ok(())
}

fn relative_path(path: &Path) -> bool {
    !path.as_os_str().is_empty() && path.components().all(|c| matches!(c, Component::Normal(_)))
}

fn validate_journal(primary: &Path, paths: &Paths, output: &Path) -> Result<(), String> {
    plain_path(&paths.journal)?;
    plain_path(&paths.tail)?;
    if !paths.exists() {
        if paths.tail.exists() {
            return Err("A saved archive index exists without its journal. Preserve it and recover the interrupted extraction before continuing.".into());
        }
        return Ok(());
    }
    if fs::metadata(&paths.journal)
        .map_err(|e| e.to_string())?
        .len()
        > 64 * 1024 * 1024
    {
        return Err("The extraction journal is too large.".into());
    }
    let (journal, _) = Journal::load(paths).map_err(|e| format!("{e:#}"))?;
    if journal.output != output
        || journal.keep
        || journal.restoring.is_some()
        || journal.parts.first().map(|p| p.0.as_path()) != Some(primary)
    {
        return Err("This journal belongs to a different extraction. Resume in its original destination using the original extraction mode.".into());
    }
    let mut seen = std::collections::HashSet::new();
    for (part, _) in &journal.parts {
        plain_path(part)?;
        if part.parent() != primary.parent()
            || hatchzip_core::archive::storage::first_volume(part) != primary
            || !seen.insert(part)
        {
            return Err("The journal contains an invalid archive volume path.".into());
        }
    }
    for link in &journal.links {
        if !relative_path(&link.path) || !relative_path(&link.target) {
            return Err("The journal contains an unsafe link path.".into());
        }
    }
    Ok(())
}

/// HatchZip decodes FreeAce members through the bundled Gleipnir sidecar.
fn configure_freeace_decoder(app: &tauri::AppHandle) -> Result<(), String> {
    use tauri_plugin_shell::ShellExt;
    let command: std::process::Command = app
        .shell()
        .sidecar("gleipnir")
        .map_err(|e| e.to_string())?
        .into();
    hatchzip_core::archive::freeace::set_decoder(PathBuf::from(command.get_program()));
    Ok(())
}

/// Recovery records sit between segments and are truncated with them, so a
/// damaged segment found halfway through could no longer be rebuilt. A full
/// checksum scrub first (disk speed, nothing decoded) means that cannot
/// happen. Only a fresh run can be scrubbed: a resumed archive has lost its
/// end, and every member is still verified as it is decoded.
fn scrub_freeace(app: &tauri::AppHandle, archive: &Path) -> Result<(), String> {
    use tauri_plugin_shell::ShellExt;
    let mut command: std::process::Command = app
        .shell()
        .sidecar("gleipnir")
        .map_err(|e| e.to_string())?
        .args(["t", "-q"])
        .arg(archive.as_os_str())
        .into();
    let output = command.output().map_err(|e| e.to_string())?;
    if output.status.success() {
        return Ok(());
    }
    Err(format!(
        "This FreeAce archive did not pass its integrity check, so it was not consumed. Extract it normally (or repair it) instead. {}",
        String::from_utf8_lossy(&output.stderr).trim()
    ))
}

fn open(request: &Request, writable: bool) -> Result<(State, Paths), String> {
    plain_path(&request.archive)?;
    plain_path(&request.destination)?;
    let (primary, paths) = session::locate(&request.archive).map_err(|e| format!("{e:#}"))?;
    plain_path(&primary)?;
    if primary.starts_with(&request.destination) || request.destination.parent().is_none() {
        return Err("Choose a destination folder that does not contain the source archive.".into());
    }
    validate_journal(&primary, &paths, &request.destination)?;
    let state = session::open(
        &primary,
        &paths,
        Some(request.destination.clone()),
        writable,
        request.password.as_deref(),
        &Arc::default(),
    )
    .map_err(|e| format!("{e:#}"))?;
    if let State::Fresh(job, _) | State::Resume(job, _) = &state {
        if !matches!(
            job.format_name,
            "zip" | "7z" | "tar" | "tar.gz" | hatchzip_core::archive::freeace::NAME
        ) {
            return Err("In-place extraction supports ZIP, 7z, tar, tar.gz and FreeAce archives, plus numbered ZIP and 7z split volumes. Use ordinary extraction for this format.".into());
        }
        for part in job.storage.parts() {
            plain_path(&part.path)?;
        }
        for item in job.units.iter().flat_map(|u| &u.items) {
            if item.path.components().any(|c| {
                c.as_os_str()
                    .to_string_lossy()
                    .to_ascii_lowercase()
                    .ends_with(".hatchzip-part")
            }) {
                return Err(
                    "An archive member conflicts with the extraction temporary-file suffix.".into(),
                );
            }
            plain_path(&request.destination.join(&item.path))?;
            let mut part = request.destination.join(&item.path).into_os_string();
            part.push(".hatchzip-part");
            let part = PathBuf::from(part);
            plain_path(&part)?;
            if matches!(state, State::Fresh(..)) && part.exists() {
                return Err(format!("{} already exists. Choose another destination or move the file before extracting.", part.display()));
            }
        }
        if let Some(reason) = job.in_place_refusal() {
            return Err(reason);
        }
        job.check_password().map_err(|e| format!("{e:#}"))?;
    }
    Ok((state, paths))
}

fn stamps(state: &State, paths: &Paths) -> Result<Vec<Stamp>, String> {
    use sha2::Digest;
    let parts = match state {
        State::Fresh(job, _) | State::Resume(job, _) => job.storage.record(),
        State::Orphan(journal) => journal.parts.clone(),
    };
    parts
        .into_iter()
        .map(|p| (p.0, false))
        .chain([(paths.journal.clone(), true), (paths.tail.clone(), true)])
        .map(|(path, hash)| {
            let present = path.try_exists().map_err(|e| e.to_string())?;
            let identity = if present {
                Some(process::archive_identity_token_from_open_file(
                    &path,
                    &crate::path_safety::open_regular_file_nofollow(&path)
                        .map_err(|e| e.to_string())?,
                )?)
            } else {
                None
            };
            let content = if present && hash {
                Some(sha2::Sha256::digest(fs::read(&path).map_err(|e| e.to_string())?).into())
            } else {
                None
            };
            Ok(Stamp {
                path,
                identity,
                content,
            })
        })
        .collect()
}

#[tauri::command]
pub async fn plan_in_place_extract(
    app: tauri::AppHandle,
    window: tauri::Window,
    request_json: String,
) -> Result<Review, String> {
    if request_json.len() > 128 * 1024 {
        return Err("Extraction request is too large.".into());
    }
    let request: Request = serde_json::from_str(&request_json).map_err(|e| e.to_string())?;
    let owner = window.label().to_owned();
    let slot = Slot::claim(app.clone(), &owner)?;
    configure_freeace_decoder(&app)?;
    tauri::async_runtime::spawn_blocking(move || {
        let _slot = slot;
        let (state, paths) = open(&request, false)?;
        let resuming = !matches!(state, State::Fresh(..));
        if let State::Fresh(job, _) = &state {
            if job.format_name == hatchzip_core::archive::freeace::NAME {
                scrub_freeace(&app, job.storage.primary())?;
            }
        }
        let plan = match &state {
            State::Fresh(job, out) => Some(session::plan(job, out, false, KEEP_FREE, false)),
            State::Resume(job, j) => Some(session::plan(job, &j.output, true, KEEP_FREE, false)),
            State::Orphan(_) => None,
        }
        .transpose()
        .map_err(|e| format!("{e:#}"))?;
        let mut random = [0u8; 24];
        getrandom::fill(&mut random).map_err(|e| e.to_string())?;
        let token = random
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect::<String>();
        let review = Review {
            token: token.clone(),
            free: plan.as_ref().map_or(0, |p| p.free),
            needed: plan.as_ref().map_or(0, |p| p.needed),
            normal_needs: plan.as_ref().map_or(0, |p| p.normal_needs),
            keep_free: KEEP_FREE,
            fits: plan.as_ref().is_none_or(|p| p.fits),
            reclaims: plan.as_ref().is_none_or(|p| p.reclaims),
            resuming,
            archive_kept_whole: plan.as_ref().is_some_and(|p| p.archive_kept_whole),
        };
        if _slot.stop.load(Ordering::Relaxed) {
            return Err("Planning cancelled. The archive was not modified.".into());
        }
        let pending = Pending {
            token,
            owner,
            created: Instant::now(),
            stamps: stamps(&state, &paths)?,
            request,
        };
        *app.state::<InPlacePlans>()
            .0
            .lock()
            .map_err(|_| "Plan lock poisoned")? = Some(pending);
        Ok(review)
    })
    .await
    .map_err(|e| e.to_string())?
}

#[tauri::command]
pub fn discard_in_place_plan(
    app: tauri::AppHandle,
    window: tauri::Window,
    token: String,
) -> Result<(), String> {
    let state = app.state::<InPlacePlans>();
    let mut pending = state.0.lock().map_err(|_| "Plan lock poisoned")?;
    if pending
        .as_ref()
        .is_some_and(|p| p.token == token && p.owner == window.label())
    {
        *pending = None;
    }
    Ok(())
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
pub struct ResultInfo {
    paused: bool,
}

#[tauri::command]
pub async fn run_in_place_extract(
    app: tauri::AppHandle,
    window: tauri::Window,
    token: String,
) -> Result<ResultInfo, String> {
    let slot = Slot::claim(app.clone(), window.label())?;
    configure_freeace_decoder(&app)?;
    let pending = {
        let state = app.state::<InPlacePlans>();
        let mut pending = state.0.lock().map_err(|_| "Plan lock poisoned")?;
        if !pending.as_ref().is_some_and(|p| {
            p.token == token
                && p.owner == window.label()
                && p.created.elapsed() < Duration::from_secs(600)
        }) {
            return Err("This space estimate expired. Review the extraction again.".into());
        }
        pending.take().unwrap()
    };
    tauri::async_runtime::spawn_blocking(move || {
        let slot = slot;
        // Recheck the read-only view before a writable resume can reconcile
        // a committed truncation left by an interrupted run.
        let (view, paths) = open(&pending.request, false)?;
        if stamps(&view, &paths)? != pending.stamps {
            return Err("The archive or recovery files changed after the estimate. Review the extraction again.".into());
        }
        drop(view);
        if slot.stop.load(Ordering::Relaxed) { return Ok(ResultInfo { paused: true }); }
        let (state, paths) = open(&pending.request, true)?;
        let paused = execute(state, paths, &slot.stop, move |percent| {
            let _ = window.emit("7z-progress-structured", serde_json::json!({"percent": percent}));
        })?;
        if !paused {
            crate::launch::remember_openable_directory(&slot.app, &pending.request.destination);
        }
        Ok(ResultInfo { paused })
    }).await.map_err(|e| e.to_string())?
}

fn execute(
    state: State,
    paths: Paths,
    stop: &AtomicBool,
    progress: impl Fn(f64) + Sync + 'static,
) -> Result<bool, String> {
    let (job, journal) = match state {
        State::Fresh(job, out) => {
            let plan =
                session::plan(&job, &out, false, KEEP_FREE, false).map_err(|e| format!("{e:#}"))?;
            if !plan.fits {
                return Err("There is not enough free space for this extraction. The archive was not modified.".into());
            }
            if stop.load(Ordering::Relaxed) {
                return Ok(true);
            }
            let journal = session::begin(&job, &paths, out, false).map_err(|e| format!("{e:#}"))?;
            (job, journal)
        }
        State::Resume(job, journal) => {
            let plan = session::plan(&job, &journal.output, true, KEEP_FREE, false)
                .map_err(|e| format!("{e:#}"))?;
            if !plan.fits {
                return Err("Free more space before resuming this extraction. Keep the output and recovery files.".into());
            }
            session::clear_uncommitted(&job, &journal.output).map_err(|e| format!("{e:#}"))?;
            (job, journal)
        }
        State::Orphan(journal) => {
            engine::finish_orphaned(&journal, &paths).map_err(|e| format!("{e:#}"))?;
            return Ok(false);
        }
    };
    let total = journal.total_size.max(1);
    let done = AtomicU64::new(journal.total_size.saturating_sub(job.remaining_size()));
    let last = AtomicU64::new(u64::MAX);
    let run = Run {
        job,
        journal,
        paths,
        stop,
        keep_free: KEEP_FREE,
        progress: Progress {
            on_bytes: Box::new(move |bytes| {
                let bytes = done
                    .fetch_add(bytes, Ordering::Relaxed)
                    .saturating_add(bytes);
                let percent = (bytes as f64 / total as f64 * 100.0).min(99.0) as u64;
                if last.swap(percent, Ordering::Relaxed) != percent {
                    progress(percent as f64);
                }
            }),
            on_commit: Box::new(|_| {}),
        },
    };
    run.execute().map(|o| matches!(o, Outcome::Paused)).map_err(|e| format!("{e:#}\n\nKeep the extracted files, .hatchzip journal and .hatchzip-tail file together. Select the first archive volume and the same destination with in-place extraction enabled to resume."))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    fn workspace(tag: &str) -> PathBuf {
        let mut random = [0; 8];
        getrandom::fill(&mut random).unwrap();
        let dir = std::env::temp_dir().join(format!(
            "freeace-in-place-{tag}-{}",
            u64::from_le_bytes(random)
        ));
        fs::create_dir(&dir).unwrap();
        dir
    }

    fn fixture(dir: &Path, name: &str) -> Request {
        let source = Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("../zips")
            .join(name);
        let archive = dir.join(name);
        fs::copy(source, &archive).unwrap();
        Request {
            archive,
            destination: dir.join("output"),
            password: None,
        }
    }

    #[test]
    fn real_archives_extract_without_an_input_snapshot() {
        for name in [
            "hello.zip",
            "hello.7z",
            "hello.tar",
            "hello.tar.gz",
            "encrypted.7z",
            "encrypted-aes.zip",
        ] {
            let dir = workspace("formats");
            let mut request = fixture(&dir, name);
            if name.starts_with("encrypted") {
                request.password = Some("freeace-test".into());
            }
            let before = fs::read(&request.archive).unwrap();
            let (view, paths) = open(&request, false).unwrap();
            assert_eq!(fs::read(&request.archive).unwrap(), before);
            assert!(!paths.exists());
            assert!(!request.destination.exists());
            assert!(!stamps(&view, &paths).unwrap().is_empty());
            drop(view);
            let (state, paths) = open(&request, true).unwrap();
            assert!(!execute(state, paths, &AtomicBool::new(false), |_| {}).unwrap());
            assert_eq!(
                fs::read(request.destination.join("hello.txt")).unwrap(),
                b"freeace fixture payload\n"
            );
            assert!(!request.archive.exists());
            assert!(!Paths::for_archive(&request.archive).journal.exists());
            assert!(!Paths::for_archive(&request.archive).tail.exists());
            fs::remove_dir_all(dir).unwrap();
        }
    }

    #[test]
    fn cancellation_preserves_archive_and_resumes_from_journal() {
        let dir = workspace("resume");
        let request = fixture(&dir, "hello.zip");
        let State::Fresh(job, output) = open(&request, true).unwrap().0 else {
            panic!()
        };
        let paths = Paths::for_archive(&request.archive);
        let journal = session::begin(&job, &paths, output, false).unwrap();
        let paused = Run {
            job,
            journal,
            paths,
            stop: &AtomicBool::new(true),
            keep_free: KEEP_FREE,
            progress: Progress {
                on_bytes: Box::new(|_| {}),
                on_commit: Box::new(|_| {}),
            },
        }
        .execute()
        .unwrap();
        assert!(matches!(paused, Outcome::Paused));
        // A different destination must never reuse this journal.
        let mut wrong = request.clone();
        wrong.destination = dir.join("wrong");
        assert!(open(&wrong, true).is_err());
        let (state, paths) = open(&request, true).unwrap();
        assert!(matches!(state, State::Resume(..)));
        assert!(!execute(state, paths, &AtomicBool::new(false), |_| {}).unwrap());
        assert_eq!(
            fs::read(request.destination.join("hello.txt")).unwrap(),
            b"freeace fixture payload\n"
        );
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn refuses_collisions_wrong_password_and_unsupported_formats_without_consuming_source() {
        for name in [
            "hello.zip",
            "encrypted-aes.zip",
            "encrypted.7z",
            "hello.rar",
            "truncated.zip",
        ] {
            let dir = workspace("refusal");
            let request = fixture(&dir, name);
            let before = fs::read(&request.archive).unwrap();
            fs::create_dir(&request.destination).unwrap();
            fs::write(request.destination.join("hello.txt"), b"keep this").unwrap();
            assert!(open(&request, true).is_err(), "{name}");
            assert_eq!(fs::read(&request.archive).unwrap(), before);
            assert_eq!(
                fs::read(request.destination.join("hello.txt")).unwrap(),
                b"keep this"
            );
            fs::remove_dir_all(dir).unwrap();
        }
    }

    #[test]
    fn split_archive_extracts_from_any_volume_and_removes_all_parts() {
        for name in ["hello.zip", "hello.7z"] {
            let dir = workspace("split");
            let source = fs::read(
                Path::new(env!("CARGO_MANIFEST_DIR"))
                    .join("../zips")
                    .join(name),
            )
            .unwrap();
            for (i, chunk) in source.chunks(50).enumerate() {
                fs::write(dir.join(format!("{name}.{:03}", i + 1)), chunk).unwrap();
            }
            let request = Request {
                archive: dir.join(format!("{name}.002")),
                destination: dir.join("output"),
                password: None,
            };
            let (state, paths) = open(&request, true).unwrap();
            assert!(!execute(state, paths, &AtomicBool::new(false), |_| {}).unwrap());
            assert_eq!(
                fs::read(request.destination.join("hello.txt")).unwrap(),
                b"freeace fixture payload\n"
            );
            assert!(fs::read_dir(&dir).unwrap().all(|e| !e
                .unwrap()
                .file_name()
                .to_string_lossy()
                .starts_with(&format!("{name}."))));
            fs::remove_dir_all(dir).unwrap();
        }
    }

    #[test]
    fn archive_replacement_changes_review_identity() {
        let dir = workspace("identity");
        let request = fixture(&dir, "hello.tar");
        let (state, paths) = open(&request, false).unwrap();
        let before = stamps(&state, &paths).unwrap();
        drop(state);
        fs::rename(&request.archive, dir.join("original.tar")).unwrap();
        fs::copy(dir.join("original.tar"), &request.archive).unwrap();
        let (state, paths) = open(&request, false).unwrap();
        assert!(before != stamps(&state, &paths).unwrap());
        drop(state);
        fs::remove_dir_all(dir).unwrap();
    }

    // A subprocess is required: unwinding would skip the real crash window
    // between the durable journal write and the subsequent truncation.
    #[test]
    fn crash_worker() {
        let Some(request) = std::env::var_os("FREEACE_IN_PLACE_TEST_REQUEST") else {
            return;
        };
        let request: Request = serde_json::from_slice(&fs::read(request).unwrap()).unwrap();
        test_decoder();
        let (state, paths) = open(&request, true).unwrap();
        assert!(!execute(state, paths, &AtomicBool::new(false), |_| {}).unwrap());
    }

    fn payload() -> Vec<u8> {
        let mut x = 0x9e3779b97f4a7c15u64;
        (0..4_194_304)
            .map(|_| {
                x ^= x << 13;
                x ^= x >> 7;
                x ^= x << 17;
                x as u8
            })
            .collect()
    }

    fn make_archive(dir: &Path, kind: &str) -> Request {
        let archive = dir.join(format!("recovery.{kind}"));
        let data = payload();
        if kind == "tar" {
            let mut writer = tar::Builder::new(fs::File::create(&archive).unwrap());
            for i in 0..3 {
                let mut header = tar::Header::new_gnu();
                header.set_size(data.len() as u64);
                header.set_mode(0o644);
                header.set_mtime(0);
                writer
                    .append_data(&mut header, format!("data/{i}.bin"), data.as_slice())
                    .unwrap();
            }
            writer.finish().unwrap();
        } else {
            let mut writer = zip::ZipWriter::new(fs::File::create(&archive).unwrap());
            let method = if kind == "deflate.zip" {
                zip::CompressionMethod::Deflated
            } else {
                zip::CompressionMethod::Stored
            };
            for i in 0..3 {
                writer
                    .start_file(
                        format!("data/{i}.bin"),
                        zip::write::SimpleFileOptions::default().compression_method(method),
                    )
                    .unwrap();
                writer.write_all(&data).unwrap();
            }
            writer.finish().unwrap();
        }
        Request {
            archive,
            destination: dir.join("output"),
            password: None,
        }
    }

    fn crash_then_resume(kind: &str, point: &str, stream: bool) {
        let dir = workspace("crash");
        let request = make_archive(&dir, kind);
        if stream {
            let State::Fresh(job, _) = open(&request, false).unwrap().0 else {
                panic!()
            };
            if job.punch_support().is_none() {
                eprintln!("stream crash test unavailable: this filesystem cannot reclaim holes");
                drop(job);
                fs::remove_dir_all(dir).unwrap();
                return;
            }
        }
        let request_file = dir.join("request.json");
        fs::write(
            &request_file,
            serde_json::to_vec(&serde_json::json!({
                "archive": request.archive, "destination": request.destination,
            }))
            .unwrap(),
        )
        .unwrap();
        let mut worker = std::process::Command::new(std::env::current_exe().unwrap());
        worker
            .args(["--exact", "in_place::tests::crash_worker", "--nocapture"])
            .env("FREEACE_IN_PLACE_TEST_REQUEST", &request_file)
            .env("HATCHZIP_BATCH_ENTRIES", "1")
            .env("HATCHZIP_CRASH_AT", point)
            .env("HATCHZIP_CRASH_AFTER", "1");
        if stream {
            worker.env("HATCHZIP_STREAM_STEP", "65536");
        }
        let crashed = worker.output().unwrap();
        assert!(!crashed.status.success(), "worker did not crash at {point}");
        assert!(
            Paths::for_archive(&request.archive).exists(),
            "worker failed before journaling: {}",
            String::from_utf8_lossy(&crashed.stderr)
        );
        let (state, paths) = open(&request, true).unwrap();
        assert!(!execute(state, paths, &AtomicBool::new(false), |_| {}).unwrap());
        for i in 0..3 {
            assert_eq!(
                fs::read(request.destination.join(format!("data/{i}.bin"))).unwrap(),
                payload()
            );
        }
        assert!(!request.archive.exists());
        fs::remove_dir_all(dir).unwrap();
    }

    /// The sidecar `npm run prepare:freeace` builds; FreeAce tests skip
    /// (pass) without it, like the bundled 7-Zip roundtrip tests.
    fn test_decoder() -> Option<PathBuf> {
        let triple = match (std::env::consts::OS, std::env::consts::ARCH) {
            ("windows", arch) => format!("{arch}-pc-windows-msvc.exe"),
            ("macos", arch) => format!("{arch}-apple-darwin"),
            (_, arch) => format!("{arch}-unknown-linux-gnu"),
        };
        let path = Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("binaries")
            .join(format!("gleipnir-{triple}"));
        path.is_file().then(|| {
            hatchzip_core::archive::freeace::set_decoder(path.clone());
            path
        })
    }

    fn freeace_expected() -> Vec<(&'static str, Vec<u8>)> {
        let data = payload();
        let reversed: Vec<u8> = data.iter().rev().copied().collect();
        vec![
            ("a.bin", data.clone()),
            // Identical to a.bin, so Gleipnir stores it once and both share a unit.
            ("copy-of-a.bin", data),
            ("nested/c.bin", reversed),
            ("empty.txt", Vec::new()),
            ("nested/note.txt", b"freeace fixture payload\n".to_vec()),
        ]
    }

    /// With `sfx`, the archive is appended to the bundled FreeAce SFX stub
    /// and named `.exe`, as SFX compression publishes it.
    fn make_freeace(dir: &Path, gleipnir: &Path, sfx: bool) -> Request {
        let source = dir.join("source");
        for (name, bytes) in freeace_expected() {
            let path = source.join(name);
            fs::create_dir_all(path.parent().unwrap()).unwrap();
            fs::write(path, bytes).unwrap();
        }
        let archive = dir.join("recovery.freeace");
        let made = std::process::Command::new(gleipnir)
            .args(["c", "-1", "-q"])
            .arg(&archive)
            .arg(&source)
            .output()
            .unwrap();
        assert!(
            made.status.success(),
            "{}",
            String::from_utf8_lossy(&made.stderr)
        );
        let archive = if sfx {
            let exe = dir.join("recovery.exe");
            let mut joined = fs::read(gleipnir.with_file_name("freeace.sfx")).unwrap();
            joined.extend(fs::read(&archive).unwrap());
            fs::write(&exe, joined).unwrap();
            fs::remove_file(&archive).unwrap();
            exe
        } else {
            archive
        };
        Request {
            archive,
            destination: dir.join("output"),
            password: None,
        }
    }

    fn assert_freeace_output(request: &Request) {
        for (name, bytes) in freeace_expected() {
            assert_eq!(
                fs::read(request.destination.join(name)).unwrap(),
                bytes,
                "{name}"
            );
        }
        assert!(!request.archive.exists());
        assert!(!Paths::for_archive(&request.archive).journal.exists());
        assert!(!Paths::for_archive(&request.archive).tail.exists());
    }

    #[test]
    fn freeace_extracts_in_place_with_shared_and_empty_members() {
        let Some(gleipnir) = test_decoder() else {
            return;
        };
        for sfx in [false, true] {
            let dir = workspace("freeace");
            let request = make_freeace(&dir, &gleipnir, sfx);
            let before = fs::read(&request.archive).unwrap();
            let (view, paths) = open(&request, false).unwrap();
            let State::Fresh(job, _) = &view else {
                panic!()
            };
            assert_eq!(job.format_name, "freeace");
            // a.bin and its duplicate decode from one segment run: one unit.
            assert_eq!(job.units.len(), 3);
            assert_eq!(fs::read(&request.archive).unwrap(), before);
            drop(view);
            drop(paths);
            let (state, paths) = open(&request, true).unwrap();
            assert!(!execute(state, paths, &AtomicBool::new(false), |_| {}).unwrap());
            assert_freeace_output(&request);
            fs::remove_dir_all(dir).unwrap();
        }
    }

    #[test]
    fn freeace_crashes_after_output_and_journal_commits_resume_byte_for_byte() {
        let Some(gleipnir) = test_decoder() else {
            return;
        };
        for (sfx, point) in [
            (false, "after-extract"),
            (false, "after-journal"),
            (true, "after-extract"),
            (true, "after-journal"),
        ] {
            let dir = workspace("freeace-crash");
            let request = make_freeace(&dir, &gleipnir, sfx);
            let request_file = dir.join("request.json");
            fs::write(
                &request_file,
                serde_json::to_vec(&serde_json::json!({
                    "archive": request.archive, "destination": request.destination,
                }))
                .unwrap(),
            )
            .unwrap();
            let crashed = std::process::Command::new(std::env::current_exe().unwrap())
                .args(["--exact", "in_place::tests::crash_worker", "--nocapture"])
                .env("FREEACE_IN_PLACE_TEST_REQUEST", &request_file)
                .env("HATCHZIP_BATCH_ENTRIES", "1")
                .env("HATCHZIP_CRASH_AT", point)
                .env("HATCHZIP_CRASH_AFTER", "1")
                .output()
                .unwrap();
            assert!(!crashed.status.success(), "worker did not crash at {point}");
            assert!(
                Paths::for_archive(&request.archive).exists(),
                "worker failed before journaling: {}",
                String::from_utf8_lossy(&crashed.stderr)
            );
            // The archive has lost its index by now; resume reads the saved one.
            let (state, paths) = open(&request, true).unwrap();
            assert!(matches!(state, State::Resume(..)), "{point}");
            assert!(!execute(state, paths, &AtomicBool::new(false), |_| {}).unwrap());
            assert_freeace_output(&request);
            fs::remove_dir_all(dir).unwrap();
        }
    }

    #[test]
    fn freeace_member_failing_verification_consumes_nothing_and_publishes_no_bad_data() {
        let Some(gleipnir) = test_decoder() else {
            return;
        };
        let dir = workspace("freeace-bad-segment");
        let request = make_freeace(&dir, &gleipnir, false);
        let mut bytes = fs::read(&request.archive).unwrap();
        // The byte just below the index belongs to the highest segment, which
        // is the first unit extracted.
        let idxoff = u64::from_le_bytes(bytes[24..32].try_into().unwrap()) as usize;
        bytes[idxoff - 64] ^= 0xff;
        fs::write(&request.archive, &bytes).unwrap();
        let (state, paths) = open(&request, true).unwrap();
        assert!(execute(state, paths, &AtomicBool::new(false), |_| {}).is_err());
        assert_eq!(fs::read(&request.archive).unwrap(), bytes);
        // Members decoded before the failure stay as uncommitted output, which
        // the next run clears; nothing unverified may appear under a real name.
        let expected: std::collections::HashMap<_, _> = freeace_expected()
            .into_iter()
            .map(|(name, data)| (request.destination.join(name), data))
            .collect();
        let published = walk_files(&request.destination);
        assert!(published.len() < expected.len(), "{published:?}");
        for path in published {
            assert_eq!(
                Some(&fs::read(&path).unwrap()),
                expected.get(&path),
                "{path:?}"
            );
        }
        fs::remove_dir_all(dir).unwrap();
    }

    fn walk_files(root: &Path) -> Vec<PathBuf> {
        let mut out = Vec::new();
        let mut stack = vec![root.to_path_buf()];
        while let Some(dir) = stack.pop() {
            let Ok(entries) = fs::read_dir(&dir) else {
                continue;
            };
            for entry in entries.flatten() {
                let path = entry.path();
                if path.is_dir() {
                    stack.push(path);
                } else {
                    out.push(path);
                }
            }
        }
        out
    }

    #[test]
    fn self_extracting_7z_is_refused_clearly_and_untouched() {
        let seven = Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("binaries")
            .join(format!("7z-{}-pc-windows-msvc.exe", std::env::consts::ARCH));
        let module = seven.with_file_name("7z.sfx");
        if !seven.is_file() || !module.is_file() {
            return;
        }
        let dir = workspace("7z-sfx");
        fs::write(
            dir.join("hello.txt"),
            b"freeace fixture payload
",
        )
        .unwrap();
        let archive = dir.join("hello.exe");
        let made = std::process::Command::new(&seven)
            .current_dir(&dir)
            .args(["a", "-t7z", "-y"])
            .arg(format!("-sfx{}", module.display()))
            .arg(&archive)
            .args(["--", "hello.txt"])
            .output()
            .unwrap();
        assert!(made.status.success());
        let before = fs::read(&archive).unwrap();
        let request = Request {
            archive: archive.clone(),
            destination: dir.join("output"),
            password: None,
        };
        let error = open(&request, true).err().expect("7z SFX must be refused");
        assert!(error.contains("self-extracting 7z"), "{error}");
        assert_eq!(fs::read(&archive).unwrap(), before);
        assert!(!request.destination.exists());
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn freeace_with_a_corrupt_index_is_refused_untouched() {
        let Some(gleipnir) = test_decoder() else {
            return;
        };
        let dir = workspace("freeace-corrupt");
        let request = make_freeace(&dir, &gleipnir, false);
        let mut bytes = fs::read(&request.archive).unwrap();
        let last = bytes.len() - 30;
        bytes[last] ^= 0xff;
        fs::write(&request.archive, &bytes).unwrap();
        assert!(open(&request, true).is_err());
        assert_eq!(fs::read(&request.archive).unwrap(), bytes);
        assert!(!request.destination.exists());
        fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn crashes_after_output_and_journal_commits_resume_byte_for_byte() {
        for kind in ["zip", "deflate.zip", "tar"] {
            for point in ["after-extract", "after-journal"] {
                crash_then_resume(kind, point, false);
            }
        }
    }

    #[test]
    fn crashes_around_stream_checkpoints_resume_byte_for_byte() {
        for kind in ["zip", "deflate.zip", "tar"] {
            for point in [
                "stream-after-sync",
                "stream-after-journal",
                "stream-after-punch",
            ] {
                crash_then_resume(kind, point, true);
            }
        }
    }
}
