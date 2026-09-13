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
        self.exit.as_ref().and_then(|payload| payload.code).unwrap_or(-1)
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
                append_limited_output(&mut out.stdout, &chunk, max_bytes, &mut out.stdout_truncated);
            }
            CommandEvent::Stderr(line) => {
                let chunk = stderr_decoder.push(&line);
                if last_progress_emit.elapsed() >= std::time::Duration::from_millis(75) {
                    if let Some(update) = parse_freeace_progress_line(&chunk) {
                        let _ = window.emit("7z-progress-structured", update);
                        last_progress_emit = std::time::Instant::now();
                    }
                }
                append_limited_output(&mut out.stderr, &chunk, max_bytes, &mut out.stderr_truncated);
            }
            CommandEvent::Terminated(payload) => {
                let stdout_tail = stdout_decoder.finish();
                append_limited_output(&mut out.stdout, &stdout_tail, max_bytes, &mut out.stdout_truncated);
                let stderr_tail = stderr_decoder.finish();
                append_limited_output(&mut out.stderr, &stderr_tail, max_bytes, &mut out.stderr_truncated);
                out.exit = Some(payload);
                break;
            }
            CommandEvent::Error(error) => {
                let detail = format!("freeace process error: {error}");
                append_limited_output(&mut out.stderr, &detail, max_bytes, &mut out.stderr_truncated);
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
    let args = parse_run_freeace_request(&request_json)?;

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
        .args(args);
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

    let collected = collect_freeace_output(&mut rx, MAX_OUTPUT_BYTES, &window).await;

    if collected.stream_error.is_some() || collected.exit.is_none() {
        let _ = terminate_child(&child);
    }
    if let Ok(mut slot) = lock_freeace(&state) {
        slot.take();
    }

    Ok(RunResult {
        stdout: sanitize_output(&collected.stdout),
        stderr: sanitize_output(&collected.stderr),
        code: collected.exit_code(),
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
