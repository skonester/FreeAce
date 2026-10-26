//! Gleipnir ("freeace") sidecar process: compress/extract/list/test via the
//! bundled `freeace` binary.
//!
//! Gleipnir has no update-in-place mode and no password support, so this
//! intentionally does not share 7z's staged commit/rollback/journal/quota
//! pipeline (`staging`/`journal`/`commit`/`quota`) -- there is nothing here
//! for that machinery to protect against. Compress and extract each write to
//! a temp path beside the destination and rename/move into place only on a
//! clean exit; a failed or cancelled run leaves the destination untouched and
//! the temp path is removed.
//!
//! Only one freeace operation may run at a time, tracked by its own
//! independent slot (`RunningFreeace`) rather than sharing 7z's `RunningProcess`
//! state machine, which carries update-reservation and staged-recovery
//! concepts that do not apply here.

use shared_child::SharedChild;
use std::{
    process::Stdio,
    sync::{Arc, Mutex, RwLock},
};
use tauri::async_runtime::{block_on, channel, Receiver};
use tauri::Emitter;
use tauri_plugin_shell::process::{CommandEvent, TerminatedPayload};
use tauri_plugin_shell::ShellExt;

use crate::output::{append_limited_output, sanitize_output, Utf8StreamDecoder, MAX_OUTPUT_BYTES};
use crate::progress::parse_freeace_progress_line;
use crate::validation::validate_run_freeace_args;

use super::commands::{is_non_running_kill_error, read_command_stream, terminate_child};
use super::RunResult;

pub struct RunningFreeace(Mutex<Option<Arc<SharedChild>>>);

impl Default for RunningFreeace {
    fn default() -> Self {
        Self::new()
    }
}

impl RunningFreeace {
    pub fn new() -> Self {
        RunningFreeace(Mutex::new(None))
    }
}

fn lock_freeace(
    state: &RunningFreeace,
) -> Result<std::sync::MutexGuard<'_, Option<Arc<SharedChild>>>, String> {
    state
        .0
        .lock()
        .map_err(|_| "Freeace process lock poisoned".to_string())
}

#[derive(serde::Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct RunFreeaceRequest {
    args: Vec<String>,
}

const MAX_RUN_FREEACE_REQUEST_BYTES: usize = 16 * 1024 * 1024;

fn parse_run_freeace_request(request_json: &str) -> Result<Vec<String>, String> {
    if request_json.len() > MAX_RUN_FREEACE_REQUEST_BYTES {
        return Err("freeace request exceeds its aggregate IPC byte limit.".to_string());
    }
    let request: RunFreeaceRequest = serde_json::from_str(request_json)
        .map_err(|error| format!("Invalid freeace request: {error}"))?;
    validate_run_freeace_args(&request.args)?;
    Ok(request.args)
}

/// The folder a successful run leaves output in: the extract destination
/// (Gleipnir defaults to `.`, which the app never relies on), or the folder
/// holding a newly compressed archive. `args` are already validated.
fn freeace_openable_directory(args: &[String]) -> Option<std::path::PathBuf> {
    let mut positionals = args
        .iter()
        .skip(1)
        .filter(|arg| !arg.starts_with('-'))
        .map(std::path::Path::new);
    match args.first()?.as_str() {
        "x" => {
            positionals.next()?;
            positionals.next().map(std::path::Path::to_path_buf)
        }
        "c" => positionals
            .next()?
            .parent()
            .filter(|parent| !parent.as_os_str().is_empty())
            .map(std::path::Path::to_path_buf),
        _ => None,
    }
}

/// A FreeAce SFX is the bundled stub with a whole archive appended (Gleipnir
/// locates it; see `find_sfx_base` in gleipnir.c). Gleipnir writes the plain
/// archive to `staged` beside the requested `.exe`; only a clean run is
/// joined with the stub and renamed over `output`.
struct SfxPlan {
    stub: Vec<u8>,
    staged: std::path::PathBuf,
    output: std::path::PathBuf,
}

fn sibling_temp_path(target: &std::path::Path, suffix: &str) -> Result<std::path::PathBuf, String> {
    let name = target
        .file_name()
        .and_then(|name| name.to_str())
        .ok_or_else(|| "Output has an invalid file name.".to_string())?;
    let mut random = [0u8; 8];
    getrandom::fill(&mut random).map_err(|error| error.to_string())?;
    let tag: String = random.iter().map(|byte| format!("{byte:02x}")).collect();
    Ok(target.with_file_name(format!(".{name}.{tag}.{suffix}")))
}

/// Strip the validated `-sfx` marker and point Gleipnir at a staged archive.
/// The stub is verified first so a broken install fails before a long run.
fn prepare_sfx_compress(
    app: &tauri::AppHandle,
    args: &mut Vec<String>,
) -> Result<Option<SfxPlan>, String> {
    let Some(marker) = args
        .iter()
        .position(|arg| arg == crate::validation::SFX_MARKER)
    else {
        return Ok(None);
    };
    let stub = bundled_freeace_sfx_stub(app)?;
    args.remove(marker);
    let output_index = args
        .iter()
        .enumerate()
        .skip(1)
        .find(|(_, arg)| !arg.starts_with('-'))
        .map(|(index, _)| index)
        .ok_or_else(|| "Self-extracting archive output is missing.".to_string())?;
    let output = std::path::PathBuf::from(&args[output_index]);
    let staged = sibling_temp_path(&output, "freeace-part")?;
    args[output_index] = staged.to_string_lossy().into_owned();
    Ok(Some(SfxPlan {
        stub,
        staged,
        output,
    }))
}

#[cfg(windows)]
fn bundled_freeace_sfx_stub(app: &tauri::AppHandle) -> Result<Vec<u8>, String> {
    super::commands::verified_bundled_sfx_resource(
        app,
        "freeace.sfx",
        option_env!("FREEACE_SFX_STUB_SHA256"),
    )
    .map(|(_, bytes)| bytes)
}

#[cfg(not(windows))]
fn bundled_freeace_sfx_stub(_app: &tauri::AppHandle) -> Result<Vec<u8>, String> {
    Err("Self-extracting archives are only available on Windows.".to_string())
}

/// Join stub and archive into a sibling temp file, flush it, then rename it
/// over the output, so a failure never leaves a half-written `.exe` behind.
fn publish_sfx(plan: &SfxPlan) -> Result<(), String> {
    use std::io::Write as _;
    let partial = sibling_temp_path(&plan.output, "exe-part")?;
    let result = (|| -> std::io::Result<()> {
        let mut out = std::fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&partial)?;
        out.write_all(&plan.stub)?;
        std::io::copy(&mut std::fs::File::open(&plan.staged)?, &mut out)?;
        out.sync_all()?;
        drop(out);
        std::fs::rename(&partial, &plan.output)
    })();
    let _ = std::fs::remove_file(&plan.staged);
    if result.is_err() {
        let _ = std::fs::remove_file(&partial);
    }
    result.map_err(|error| format!("Could not write the self-extracting archive: {error}"))
}

struct CollectedFreeaceOutput {
    stdout: String,
    stderr: String,
    stdout_truncated: bool,
    stderr_truncated: bool,
    exit: Option<TerminatedPayload>,
    stream_error: Option<String>,
}

impl CollectedFreeaceOutput {
    fn exit_code(&self) -> i32 {
        if self.stream_error.is_some() {
            return -1;
        }
        self.exit
            .as_ref()
            .and_then(|payload| payload.code)
            .unwrap_or(-1)
    }
}

/// Like `commands::collect_command_output`, but progress lives on Gleipnir's
/// stderr rather than stdout (see `progress()` in gleipnir.c), so this parses
/// and live-emits from stderr chunks instead.
async fn collect_freeace_output(
    rx: &mut Receiver<CommandEvent>,
    max_bytes: usize,
    window: &tauri::Window,
) -> CollectedFreeaceOutput {
    let mut out = CollectedFreeaceOutput {
        stdout: String::new(),
        stderr: String::new(),
        stdout_truncated: false,
        stderr_truncated: false,
        exit: None,
        stream_error: None,
    };
    let mut stdout_decoder = Utf8StreamDecoder::default();
    let mut stderr_decoder = Utf8StreamDecoder::default();
    let mut last_progress_emit = std::time::Instant::now()
        .checked_sub(std::time::Duration::from_millis(100))
        .unwrap_or_else(std::time::Instant::now);

    while let Some(event) = rx.recv().await {
        match event {
            CommandEvent::Stdout(line) => {
                let chunk = stdout_decoder.push(&line);
                append_limited_output(
                    &mut out.stdout,
                    &chunk,
                    max_bytes,
                    &mut out.stdout_truncated,
                );
            }
            CommandEvent::Stderr(line) => {
                let chunk = stderr_decoder.push(&line);
                if last_progress_emit.elapsed() >= std::time::Duration::from_millis(75) {
                    if let Some(update) = parse_freeace_progress_line(&chunk) {
                        let _ = window.emit("7z-progress-structured", update);
                        last_progress_emit = std::time::Instant::now();
                    }
                }
                append_limited_output(
                    &mut out.stderr,
                    &chunk,
                    max_bytes,
                    &mut out.stderr_truncated,
                );
            }
            CommandEvent::Terminated(payload) => {
                let stdout_tail = stdout_decoder.finish();
                append_limited_output(
                    &mut out.stdout,
                    &stdout_tail,
                    max_bytes,
                    &mut out.stdout_truncated,
                );
                let stderr_tail = stderr_decoder.finish();
                append_limited_output(
                    &mut out.stderr,
                    &stderr_tail,
                    max_bytes,
                    &mut out.stderr_truncated,
                );
                out.exit = Some(payload);
                break;
            }
            CommandEvent::Error(error) => {
                let detail = format!("freeace process error: {error}");
                append_limited_output(
                    &mut out.stderr,
                    &detail,
                    max_bytes,
                    &mut out.stderr_truncated,
                );
                if out.stream_error.is_none() {
                    out.stream_error = Some(error);
                }
            }
            _ => {}
        }
    }

    out
}

#[tauri::command]
pub async fn run_freeace(
    app: tauri::AppHandle,
    window: tauri::Window,
    state: tauri::State<'_, RunningFreeace>,
    request_json: String,
) -> Result<RunResult, String> {
    let original_args = parse_run_freeace_request(&request_json)?;
    let mut args = original_args.clone();
    let sfx = prepare_sfx_compress(&app, &mut args)?;

    {
        let mut slot = lock_freeace(&state)?;
        if slot.is_some() {
            return Err("Another freeace operation is already running.".to_string());
        }
        // Reserve the slot before spawning so a second call cannot race in
        // between the check above and the spawn below.
        *slot = None;
    }

    let plugin_command = app
        .shell()
        .sidecar("gleipnir")
        .map_err(|error| error.to_string())?
        .args(&args);
    let mut command: std::process::Command = plugin_command.into();
    command
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());
    let child = match SharedChild::spawn(&mut command) {
        Ok(child) => Arc::new(child),
        Err(error) => {
            return Err(error.to_string());
        }
    };
    {
        let mut slot = lock_freeace(&state)?;
        *slot = Some(child.clone());
    }

    let setup = (|| {
        let stdout = child
            .take_stdout()
            .ok_or_else(|| "Could not capture freeace stdout.".to_string())?;
        let stderr = child
            .take_stderr()
            .ok_or_else(|| "Could not capture freeace stderr.".to_string())?;
        Ok::<_, String>((stdout, stderr))
    })();
    let (stdout, stderr) = match setup {
        Ok(streams) => streams,
        Err(error) => {
            let _ = terminate_child(&child);
            if let Ok(mut slot) = lock_freeace(&state) {
                slot.take();
            }
            if let Some(plan) = &sfx {
                let _ = std::fs::remove_file(&plan.staged);
            }
            return Err(error);
        }
    };

    let (tx, mut rx) = channel(512);
    let readers = Arc::new(RwLock::new(()));
    for (reader, wrap) in [
        (
            Box::new(stdout) as Box<dyn std::io::Read + Send>,
            CommandEvent::Stdout as fn(Vec<u8>) -> CommandEvent,
        ),
        (
            Box::new(stderr) as Box<dyn std::io::Read + Send>,
            CommandEvent::Stderr as fn(Vec<u8>) -> CommandEvent,
        ),
    ] {
        let tx = tx.clone();
        let readers = readers.clone();
        std::thread::spawn(move || {
            let _guard = readers
                .read()
                .unwrap_or_else(|poisoned| poisoned.into_inner());
            read_command_stream(reader, tx, wrap);
        });
    }
    let wait_child = child.clone();
    std::thread::spawn(move || {
        let event = match wait_child.wait() {
            Ok(status) => {
                let _guard = readers
                    .write()
                    .unwrap_or_else(|poisoned| poisoned.into_inner());
                #[cfg(unix)]
                let signal = {
                    use std::os::unix::process::ExitStatusExt;
                    status.signal()
                };
                #[cfg(windows)]
                let signal = None;
                CommandEvent::Terminated(TerminatedPayload {
                    code: status.code(),
                    signal,
                })
            }
            Err(error) => CommandEvent::Error(error.to_string()),
        };
        let _ = block_on(async move { tx.send(event).await });
    });

    let mut collected = collect_freeace_output(&mut rx, MAX_OUTPUT_BYTES, &window).await;

    if collected.stream_error.is_some() || collected.exit.is_none() {
        let _ = terminate_child(&child);
    }
    if let Ok(mut slot) = lock_freeace(&state) {
        slot.take();
    }
    let mut code = collected.exit_code();
    if let Some(plan) = &sfx {
        if code == 0 {
            if let Err(error) = publish_sfx(plan) {
                code = 1;
                collected.stderr.push_str(&format!(
                    "
{error}"
                ));
            }
        } else {
            let _ = std::fs::remove_file(&plan.staged);
        }
    }
    // 7z registers its destination when staging commits; Gleipnir writes in
    // place, so a clean exit is the equivalent point. Without this the main
    // window's "Open folder" is refused after every .freeace operation.
    if code == 0 {
        if let Some(dir) = freeace_openable_directory(&original_args) {
            crate::launch::remember_openable_directory(&app, &dir);
        }
    }

    Ok(RunResult {
        stdout: sanitize_output(&collected.stdout),
        stderr: sanitize_output(&collected.stderr),
        code,
        warning_code: None,
        stdout_truncated: collected.stdout_truncated,
        stderr_truncated: collected.stderr_truncated,
    })
}

#[tauri::command]
pub fn cancel_freeace(state: tauri::State<'_, RunningFreeace>) -> Result<bool, String> {
    let child = lock_freeace(&state)?.clone();
    match child {
        Some(child) => match terminate_child(&child) {
            Ok(()) => Ok(true),
            Err(error) => {
                if is_non_running_kill_error(&error) {
                    Ok(true)
                } else {
                    Err(error)
                }
            }
        },
        None => Ok(false),
    }
}

#[tauri::command]
pub fn is_freeace_running(state: tauri::State<'_, RunningFreeace>) -> Result<bool, String> {
    Ok(lock_freeace(&state)?.is_some())
}

#[cfg(test)]
mod tests {
    use super::{freeace_openable_directory, publish_sfx, SfxPlan};
    use std::path::PathBuf;

    fn temp_dir(tag: &str) -> PathBuf {
        let dir = std::env::temp_dir().join(format!(
            "freeace-sfx-{tag}-{}",
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        std::fs::create_dir_all(&dir).unwrap();
        dir
    }

    fn leftovers(dir: &std::path::Path) -> Vec<String> {
        std::fs::read_dir(dir)
            .unwrap()
            .map(|entry| entry.unwrap().file_name().to_string_lossy().into_owned())
            .filter(|name| name.starts_with('.'))
            .collect()
    }

    #[test]
    fn publish_sfx_joins_stub_and_archive_and_replaces_output() {
        let dir = temp_dir("publish");
        let output = dir.join("setup.exe");
        std::fs::write(&output, b"old").unwrap();
        let staged = dir.join(".setup.exe.1.freeace-part");
        std::fs::write(&staged, b"GENA-archive").unwrap();
        let plan = SfxPlan {
            stub: b"MZ-stub".to_vec(),
            staged: staged.clone(),
            output: output.clone(),
        };
        publish_sfx(&plan).expect("publish");
        assert_eq!(std::fs::read(&output).unwrap(), b"MZ-stubGENA-archive");
        assert!(leftovers(&dir).is_empty(), "{:?}", leftovers(&dir));
        let _ = std::fs::remove_dir_all(dir);
    }

    #[test]
    fn publish_sfx_failure_leaves_no_partial_output() {
        let dir = temp_dir("missing");
        let output = dir.join("setup.exe");
        let plan = SfxPlan {
            stub: b"MZ".to_vec(),
            staged: dir.join(".setup.exe.1.freeace-part"),
            output: output.clone(),
        };
        publish_sfx(&plan).expect_err("staged archive is missing");
        assert!(!output.exists());
        assert!(leftovers(&dir).is_empty(), "{:?}", leftovers(&dir));
        let _ = std::fs::remove_dir_all(dir);
    }

    fn args(values: &[&str]) -> Vec<String> {
        values.iter().map(|value| value.to_string()).collect()
    }

    #[test]
    fn openable_directory_is_extract_destination_after_flags() {
        assert_eq!(
            freeace_openable_directory(&args(&["x", "-t0", "/a/DLC.freeace", "/out/DLC"])),
            Some(PathBuf::from("/out/DLC"))
        );
        assert_eq!(
            freeace_openable_directory(&args(&["x", "/a/DLC.freeace"])),
            None
        );
    }

    #[test]
    fn openable_directory_is_compress_output_parent() {
        assert_eq!(
            freeace_openable_directory(&args(&["c", "-9", "-t4", "/out/a.freeace", "/in"])),
            Some(PathBuf::from("/out"))
        );
        assert_eq!(
            freeace_openable_directory(&args(&["c", "a.freeace", "in"])),
            None
        );
    }

    #[test]
    fn openable_directory_ignores_list_and_test() {
        assert_eq!(
            freeace_openable_directory(&args(&["l", "/a/b.freeace"])),
            None
        );
        assert_eq!(
            freeace_openable_directory(&args(&["t", "/a/b.freeace"])),
            None
        );
    }
}
