//! In-process disk-image mounting. The native backend is adapted from DIM (MIT).

use std::{
    path::{Path, PathBuf},
    sync::atomic::AtomicBool,
};

use serde::Serialize;
#[cfg(windows)]
use tauri::Manager;

#[cfg(windows)]
mod convert;
#[cfg(windows)]
mod windows;

pub const EXTENSIONS: &[&str] = &[
    "iso", "cue", "bin", "img", "raw", "nrg", "mds", "mdf", "ccd",
];

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MountInfo {
    id: u64,
    image: PathBuf,
    mount_point: Option<String>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct MountError {
    stage: &'static str,
    message: String,
    os_code: Option<i32>,
}

impl std::fmt::Display for MountError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", self.message)
    }
}

impl MountError {
    fn message(stage: &'static str, message: impl Into<String>) -> Self {
        Self {
            stage,
            message: message.into(),
            os_code: None,
        }
    }

    #[cfg(windows)]
    fn io(stage: &'static str, context: &str, error: std::io::Error) -> Self {
        Self {
            stage,
            message: format!("{context}: {error}"),
            os_code: error.raw_os_error(),
        }
    }
}

#[derive(Default)]
pub struct Mounts {
    #[cfg(windows)]
    state: std::sync::Mutex<windows::Registry>,
}

pub fn supported() -> Result<(), MountError> {
    if cfg!(windows) {
        Ok(())
    } else {
        Err(MountError::message(
            "platform",
            "Disk image mounting is available on Windows.",
        ))
    }
}

pub fn mount(
    app: &tauri::AppHandle,
    image: &Path,
    stop: &AtomicBool,
) -> Result<MountInfo, MountError> {
    supported()?;
    #[cfg(windows)]
    {
        let result = app.state::<Mounts>().mount(app, image, stop);
        match &result {
            Ok(info) => eprintln!("Mounted disk image {}", info.image.display()),
            Err(error) => eprintln!("Disk image {}: {error}", error.stage),
        }
        result
    }
    #[cfg(not(windows))]
    {
        let _ = (app, image, stop);
        unreachable!("unsupported platform was rejected")
    }
}

pub fn unmount(app: &tauri::AppHandle, id: u64) -> Result<(), MountError> {
    supported()?;
    #[cfg(windows)]
    {
        let result = app.state::<Mounts>().unmount(id);
        match &result {
            Ok(()) => eprintln!("Unmounted disk image {id}"),
            Err(error) => eprintln!("Disk image {}: {error}", error.stage),
        }
        result
    }
    #[cfg(not(windows))]
    {
        let _ = (app, id);
        unreachable!("unsupported platform was rejected")
    }
}

pub fn shutdown(_app: &tauri::AppHandle) {
    #[cfg(windows)]
    _app.state::<Mounts>().shutdown();
}

pub fn release(app: &tauri::AppHandle, info: &MountInfo) -> Result<(), MountError> {
    unmount(app, info.id)
}

#[tauri::command]
pub async fn mount_disk_image(
    app: tauri::AppHandle,
    path: PathBuf,
) -> Result<MountInfo, MountError> {
    let slot = crate::in_place::Slot::claim(app.clone(), "main")
        .map_err(|error| MountError::message("busy", error))?;
    tauri::async_runtime::spawn_blocking(move || mount(&app, &path, &slot.stop))
        .await
        .map_err(|error| MountError::message("worker", error.to_string()))?
}

#[tauri::command]
pub async fn unmount_disk_image(app: tauri::AppHandle, id: u64) -> Result<(), MountError> {
    tauri::async_runtime::spawn_blocking(move || unmount(&app, id))
        .await
        .map_err(|error| MountError::message("worker", error.to_string()))?
}

#[tauri::command]
pub async fn list_disk_images(app: tauri::AppHandle) -> Result<Vec<MountInfo>, MountError> {
    #[cfg(windows)]
    {
        tauri::async_runtime::spawn_blocking(move || app.state::<Mounts>().list())
            .await
            .map_err(|error| MountError::message("worker", error.to_string()))?
    }
    #[cfg(not(windows))]
    {
        let _ = app;
        Ok(Vec::new())
    }
}

/// Search only verified staged output. Descriptor files take priority over their
/// companion formats so a CUE/BIN or CCD/IMG pair is treated as one image.
pub fn extracted_image(root: &Path) -> Result<PathBuf, MountError> {
    let mut pending = vec![root.to_path_buf()];
    let mut images = Vec::new();
    let mut descriptors = Vec::new();
    let access = |error: std::io::Error| MountError::message("selection", error.to_string());
    while let Some(directory) = pending.pop() {
        for entry in std::fs::read_dir(directory).map_err(access)? {
            let entry = entry.map_err(access)?;
            let path = entry.path();
            let metadata = std::fs::symlink_metadata(&path).map_err(access)?;
            if crate::path_safety::is_link_or_reparse(&metadata) {
                continue;
            }
            if metadata.is_dir() {
                pending.push(path);
            } else if metadata.is_file() {
                let ext = path
                    .extension()
                    .and_then(|s| s.to_str())
                    .unwrap_or("")
                    .to_ascii_lowercase();
                match ext.as_str() {
                    "cue" | "ccd" | "mds" => descriptors.push(path),
                    ext if EXTENSIONS.contains(&ext) => images.push(path),
                    _ => {}
                }
            }
        }
    }
    #[cfg(windows)]
    for descriptor in &descriptors {
        if let Ok(companions) = convert::companion_paths(descriptor) {
            images.retain(|path| {
                !companions
                    .iter()
                    .any(|companion| path.as_os_str().eq_ignore_ascii_case(companion.as_os_str()))
            });
        }
    }
    images.extend(descriptors);
    if images.len() == 1 {
        return Ok(images.pop().unwrap().strip_prefix(root).unwrap().to_owned());
    }
    Err(MountError::message(
        "selection",
        if images.is_empty() {
            "The archive contains no mountable image. Extract it first if the image is inside another archive."
        } else {
            "The archive contains multiple disk images. Extract it with automatic mounting disabled, then choose an image in Disk images."
        },
    ))
}
