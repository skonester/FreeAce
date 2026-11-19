//! Windows optical drive attachment adapted from DIM by Glauber Lima (MIT).

use std::{
    collections::BTreeMap,
    ffi::OsStr,
    fs::File,
    io::{self, Read, Seek, SeekFrom},
    os::windows::{
        ffi::OsStrExt,
        fs::MetadataExt,
        io::{AsRawHandle, FromRawHandle, OwnedHandle},
    },
    path::{Path, PathBuf},
    ptr::{null, null_mut},
    sync::atomic::{AtomicBool, Ordering},
    time::Duration,
};

use tauri::Manager;
use windows_sys::Win32::{
    Foundation::{GetLastError, ERROR_NOT_READY},
    Storage::{
        FileSystem::{
            GetLogicalDrives, GetVolumeInformationW, QueryDosDeviceW, FILE_ATTRIBUTE_SPARSE_FILE,
        },
        Vhd::*,
    },
};

use super::{convert, MountError, MountInfo, Mounts};

#[cfg(test)]
#[path = "windows_tests.rs"]
mod tests;

pub(super) fn wide(s: &OsStr) -> Vec<u16> {
    s.encode_wide().chain([0]).collect()
}

fn from_wide(buf: &[u16]) -> String {
    String::from_utf16_lossy(&buf[..buf.iter().position(|&c| c == 0).unwrap_or(buf.len())])
}

struct Mount {
    handle: Option<OwnedHandle>,
    temp: Option<ConversionWorkspace>,
    image: PathBuf,
    device: Option<String>,
    drive: Option<char>,
    ready_seen: bool,
}

fn attach(iso: &Path) -> io::Result<OwnedHandle> {
    if iso.metadata()?.file_attributes() & FILE_ATTRIBUTE_SPARSE_FILE != 0 {
        return Err(io::Error::new(
            io::ErrorKind::Unsupported,
            "Windows cannot mount a sparse ISO",
        ));
    }
    let absolute;
    let native_path = if iso.as_os_str().encode_wide().count() >= 260 {
        absolute = iso.canonicalize()?;
        &absolute
    } else {
        iso
    };
    let path = wide(native_path.as_os_str());
    let storage = VIRTUAL_STORAGE_TYPE {
        DeviceId: VIRTUAL_STORAGE_TYPE_DEVICE_ISO,
        VendorId: VIRTUAL_STORAGE_TYPE_VENDOR_MICROSOFT,
    };
    let mut raw = null_mut();
    // Win32 takes a NUL-terminated path. OwnedHandle closes the attachment
    // on all error paths and when the process releases the last handle.
    let code = unsafe {
        OpenVirtualDisk(
            &storage,
            path.as_ptr(),
            VIRTUAL_DISK_ACCESS_READ,
            OPEN_VIRTUAL_DISK_FLAG_NONE,
            null(),
            &mut raw,
        )
    };
    if code != 0 {
        return Err(io::Error::from_raw_os_error(code as i32));
    }
    let handle = unsafe { OwnedHandle::from_raw_handle(raw) };
    // A read-only attachment without PERMANENT_LIFETIME detaches on close or process death.
    let code = unsafe {
        AttachVirtualDisk(
            raw,
            null_mut(),
            ATTACH_VIRTUAL_DISK_FLAG_READ_ONLY,
            0,
            null(),
            null(),
        )
    };
    if code != 0 {
        return Err(io::Error::from_raw_os_error(code as i32));
    }
    Ok(handle)
}

fn physical_device(handle: &OwnedHandle) -> Option<String> {
    let mut buffer = [0u16; 260];
    let mut bytes = std::mem::size_of_val(&buffer) as u32;
    if unsafe {
        GetVirtualDiskPhysicalPath(handle.as_raw_handle(), &mut bytes, buffer.as_mut_ptr())
    } != 0
    {
        return None;
    }
    from_wide(&buffer)
        .to_ascii_uppercase()
        .strip_prefix(r"\\.\CDROM")
        .map(|number| format!(r"\Device\CdRom{number}"))
}

fn find_drive(device: &str) -> Option<char> {
    let mask = unsafe { GetLogicalDrives() };
    ('A'..='Z')
        .filter(|letter| mask & (1 << (*letter as u8 - b'A')) != 0)
        .find(|letter| {
            let name = [*letter as u16, b':' as u16, 0];
            let mut buffer = [0u16; 260];
            let count =
                unsafe { QueryDosDeviceW(name.as_ptr(), buffer.as_mut_ptr(), buffer.len() as u32) };
            count != 0 && from_wide(&buffer).eq_ignore_ascii_case(device)
        })
}

fn drive_root(drive: char) -> String {
    format!("{drive}:\\")
}

impl Mount {
    fn refresh(&mut self) -> bool {
        if self.device.is_none() {
            self.device = self.handle.as_ref().and_then(physical_device);
        }
        let drive = self.device.as_deref().and_then(find_drive);
        if let Some(letter) = drive {
            let root = drive_root(letter);
            let root = wide(OsStr::new(&root));
            let ready = unsafe {
                GetVolumeInformationW(
                    root.as_ptr(),
                    null_mut(),
                    0,
                    null_mut(),
                    null_mut(),
                    null_mut(),
                    null_mut(),
                    0,
                )
            } != 0;
            if !ready && self.ready_seen && unsafe { GetLastError() } == ERROR_NOT_READY {
                return false;
            }
            self.ready_seen |= ready;
        } else if self.drive.is_some() {
            return false;
        }
        self.drive = drive;
        true
    }

    fn info(&self, id: u64) -> MountInfo {
        MountInfo {
            id,
            image: self.image.clone(),
            mount_point: self.drive.map(drive_root),
        }
    }

    fn cleanup(&mut self) -> io::Result<()> {
        drop(self.handle.take());
        if let Some(temp) = self.temp.take() {
            let ConversionWorkspace { lease, directory } = temp;
            let image = directory.path().join("image.iso");
            // The storage stack can hold the converted ISO briefly after detach.
            for _ in 0..5 {
                match remove_converted_iso(&image) {
                    Ok(()) => {
                        drop(lease);
                        return directory.close();
                    }
                    Err(_) => std::thread::sleep(Duration::from_millis(100)),
                }
            }
            let result = remove_converted_iso(&image);
            drop(lease);
            if result.is_err() {
                // Keep the lease file for the next cache sweep.
                let _ = directory.keep();
                return result;
            }
            return directory.close();
        }
        Ok(())
    }
}

impl Drop for Mount {
    fn drop(&mut self) {
        if let Err(error) = self.cleanup() {
            eprintln!(
                "Could not clean up disk image {}: {error}",
                self.image.display()
            );
        }
    }
}

#[derive(Default)]
pub(super) struct Registry {
    entries: BTreeMap<u64, Mount>,
    next_id: u64,
    closing: bool,
}

impl Registry {
    fn refresh(&mut self) {
        self.entries.retain(|_, mount| mount.refresh());
    }
}

fn validate_iso(path: &Path) -> io::Result<()> {
    let mut file = File::open(path)?;
    // ISO 9660 and UDF volume recognition sequences occupy 2048-byte sectors.
    // UDF may follow a bridge ISO descriptor sequence.
    let mut descriptor = [0; 7];
    for sector in 16..=255 {
        file.seek(SeekFrom::Start(sector * 2048))?;
        if let Err(error) = file.read_exact(&mut descriptor) {
            if error.kind() == io::ErrorKind::UnexpectedEof {
                break;
            }
            return Err(error);
        }
        if matches!(&descriptor[1..6], b"CD001" | b"NSR02" | b"NSR03") {
            return Ok(());
        }
    }
    Err(io::Error::new(
        io::ErrorKind::InvalidData,
        "No ISO 9660 or UDF volume descriptor was found",
    ))
}

fn check_cancel(stop: &AtomicBool) -> Result<(), MountError> {
    if stop.load(Ordering::Relaxed) {
        return Err(MountError::message(
            "cancelled",
            "Disk image mounting was cancelled.",
        ));
    }
    Ok(())
}

impl Mounts {
    fn lock(&self) -> Result<std::sync::MutexGuard<'_, Registry>, MountError> {
        self.state
            .lock()
            .map_err(|_| MountError::message("state", "Disk image state is unavailable."))
    }

    pub(super) fn mount(
        &self,
        app: &tauri::AppHandle,
        path: &Path,
        stop: &AtomicBool,
    ) -> Result<MountInfo, MountError> {
        check_cancel(stop)?;
        convert::validate_input(path)
            .map_err(|e| MountError::io("input", "Could not open disk image", e))?;
        let image = path
            .canonicalize()
            .map_err(|e| MountError::io("input", "Could not resolve disk image", e))?;
        {
            let mut registry = self.lock()?;
            registry.refresh();
            if let Some((&id, mount)) = registry.entries.iter().find(|(_, mount)| {
                mount
                    .image
                    .as_os_str()
                    .eq_ignore_ascii_case(image.as_os_str())
            }) {
                return Ok(mount.info(id));
            }
        }
        let source = convert::prepare(path)
            .map_err(|e| MountError::io("prepare", "Could not read disk image", e))?;
        let (iso, temp) = match source {
            convert::Source::Direct(iso) => (iso, None),
            convert::Source::Extract(plan) => {
                let cache = app
                    .path()
                    .app_cache_dir()
                    .map_err(|e| MountError::message("conversion", e.to_string()))?
                    .join("disk-images");
                let temp = conversion_workspace(&cache).map_err(|e| {
                    MountError::io("conversion", "Could not create image workspace", e)
                })?;
                let iso = temp.directory.path().join("image.iso");
                convert::extract(&plan, &iso, stop, &mut |_| {})
                    .map_err(|e| MountError::io("conversion", "Could not convert disk image", e))?;
                (iso, Some(temp))
            }
        };
        check_cancel(stop)?;
        validate_iso(&iso).map_err(|e| MountError::io("verify", "Invalid disk image", e))?;
        let handle = attach(&iso)
            .map_err(|e| MountError::io("attach", "Windows could not mount the disk image", e))?;
        let mut mount = Mount {
            handle: Some(handle),
            temp,
            image,
            device: None,
            drive: None,
            ready_seen: false,
        };
        for _ in 0..10 {
            check_cancel(stop)?;
            mount.refresh();
            if mount.drive.is_some() {
                break;
            }
            std::thread::sleep(Duration::from_millis(100));
        }
        let mut registry = self.lock()?;
        if registry.closing {
            return Err(MountError::message("cancelled", "FreeAce is exiting."));
        }
        check_cancel(stop)?;
        registry.next_id += 1;
        let id = registry.next_id;
        let info = mount.info(id);
        registry.entries.insert(id, mount);
        Ok(info)
    }

    pub(super) fn unmount(&self, id: u64) -> Result<(), MountError> {
        let mut mount = self.lock()?.entries.remove(&id).ok_or_else(|| {
            MountError::message("unmount", "This disk image is no longer mounted.")
        })?;
        mount.cleanup().map_err(|e| {
            MountError::io(
                "cleanup",
                "Drive detached, but its temporary image could not be removed",
                e,
            )
        })
    }

    pub(super) fn list(&self) -> Result<Vec<MountInfo>, MountError> {
        let mut registry = self.lock()?;
        registry.refresh();
        Ok(registry
            .entries
            .iter()
            .map(|(&id, mount)| mount.info(id))
            .collect())
    }

    pub(super) fn shutdown(&self) {
        if let Ok(mut registry) = self.state.lock() {
            registry.closing = true;
            let mounts = std::mem::take(&mut registry.entries);
            drop(registry);
            drop(mounts);
        }
    }
}

struct ConversionWorkspace {
    lease: File,
    directory: tempfile::TempDir,
}

fn conversion_workspace(cache: &Path) -> io::Result<ConversionWorkspace> {
    std::fs::create_dir_all(cache)?;
    // The lease prevents reclaiming workspaces held by another process.
    for entry in std::fs::read_dir(cache)?.flatten() {
        let name = entry.file_name();
        if !name.to_string_lossy().starts_with("dim-") {
            continue;
        }
        let Ok(metadata) = std::fs::symlink_metadata(entry.path()) else {
            continue;
        };
        if metadata.is_dir()
            && !crate::path_safety::is_link_or_reparse(&metadata)
            && metadata
                .modified()
                .ok()
                .and_then(|time| time.elapsed().ok())
                .is_some_and(|age| age >= Duration::from_secs(86_400))
        {
            let path = entry.path();
            let lock_path = path.join("lease");
            if let Ok(lease) = std::fs::OpenOptions::new()
                .read(true)
                .write(true)
                .open(&lock_path)
            {
                if lease.try_lock().is_ok() && remove_converted_iso(&path.join("image.iso")).is_ok()
                {
                    let _ = std::fs::remove_file(lock_path);
                    drop(lease);
                    let _ = std::fs::remove_dir(path);
                }
            }
        }
    }
    let directory = tempfile::Builder::new().prefix("dim-").tempdir_in(cache)?;
    let lease = std::fs::OpenOptions::new()
        .read(true)
        .write(true)
        .create_new(true)
        .open(directory.path().join("lease"))?;
    lease.lock()?;
    Ok(ConversionWorkspace { lease, directory })
}

fn remove_converted_iso(path: &Path) -> io::Result<()> {
    match std::fs::remove_file(path) {
        Err(error) if error.kind() == io::ErrorKind::NotFound => Ok(()),
        result => result,
    }
}
