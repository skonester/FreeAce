//! FreeAce (Gleipnir v2) archives: a 48-byte header, independently coded
//! segments, and an index plus trailer at the end. Added for FreeAce; not
//! part of upstream HatchZip.
//!
//! A member is decoded by the Gleipnir sidecar (`x -O -I`), which streams it
//! to stdout and verifies every segment checksum and the member's SHA-256
//! before exiting cleanly. The reader below turns a non-zero exit into a read
//! error at end of stream, so the sink discards the part file instead of
//! publishing it.
//!
//! Units are members grouped by their first segment, since a deduplicated
//! member shares an earlier member's segments. Truncation is only safe if
//! every unit's data lies below the next unit's start; `open` checks that
//! rather than trusting the writer's layout.

use std::collections::BTreeMap;
use std::io::{self, Read, Seek, SeekFrom};
use std::path::PathBuf;
use std::process::{Child, ChildStdout, Command, Stdio};
use std::sync::{Arc, OnceLock};
use std::thread::JoinHandle;
use std::time::{Duration, UNIX_EPOCH};

use anyhow::{Context, Result, anyhow, bail};

use super::storage::{Storage, Tail};
use super::{Damaged, Format, Item, Kind, Opened, Sink, Unit, Worker, safe_path};

pub const NAME: &str = "freeace";
pub const MAGIC: &[u8; 4] = b"GENA";
const VERSION: u16 = 2;
const HDR_BYTES: u64 = 48;
const TRL_BYTES: u64 = 20;
const INDEX_MAX: u64 = 1 << 30;

static DECODER: OnceLock<PathBuf> = OnceLock::new();

/// Where the Gleipnir sidecar lives. The front end sets it once before
/// opening any FreeAce archive.
pub fn set_decoder(path: PathBuf) {
    let _ = DECODER.set(path);
}

struct Member {
    name: String,
    size: u64,
    mtime: i64,
    mode: u32,
    seg0: u64,
    nseg: u64,
}

struct Seg {
    off: u64,
    clen: u64,
}

struct Freeace {
    storage: Arc<Storage>,
    /// Index plus trailer, exactly as saved before anything is reclaimed;
    /// handed to the decoder with `-I` because the archive loses it first.
    tail_bytes: Arc<Vec<u8>>,
    /// Member names per unit, in the order of the unit's items.
    names: Vec<Vec<String>>,
    /// Bytes of SFX stub before the archive; 0 for a bare archive.
    base: u64,
}

fn header_plausible(h: &[u8]) -> bool {
    &h[..4] == MAGIC && u16_at(h, 4) == VERSION && xxh64(&h[..40]) == u64_at(h, 40)
}

/// Where the FreeAce archive in a file starts: 0 for a bare archive, or the
/// end of a self-extracting stub (mirrors `find_sfx_base` in gleipnir.c).
/// `None` if the file holds no readable FreeAce archive.
///
/// The trailer gives the index's absolute position; an SFX archive starts at
/// the one header whose checksum holds and whose stored index offset lands
/// exactly there.
pub fn locate<R: Read + Seek>(r: &mut R, len: u64) -> io::Result<Option<u64>> {
    const SCAN_MAX: u64 = 64 << 20;
    const CHUNK: usize = 1 << 16;
    if len < HDR_BYTES + TRL_BYTES {
        return Ok(None);
    }
    let mut trailer = [0u8; TRL_BYTES as usize];
    r.seek(SeekFrom::Start(len - TRL_BYTES))?;
    r.read_exact(&mut trailer)?;
    let ixn = u64_at(&trailer, 8);
    if &trailer[16..] != MAGIC || ixn > len - HDR_BYTES - TRL_BYTES {
        return Ok(None);
    }
    let ixoff_abs = len - TRL_BYTES - ixn;
    let mut header = [0u8; HDR_BYTES as usize];
    r.seek(SeekFrom::Start(0))?;
    r.read_exact(&mut header)?;
    if header_plausible(&header) {
        return Ok(Some(0));
    }
    let limit = ixoff_abs.min(SCAN_MAX);
    let mut buf = vec![0u8; CHUNK + HDR_BYTES as usize];
    let mut at = 0u64;
    while at < limit {
        r.seek(SeekFrom::Start(at))?;
        let mut got = 0;
        while got < buf.len() {
            match r.read(&mut buf[got..])? {
                0 => break,
                n => got += n,
            }
        }
        let mut i = 0;
        while i + HDR_BYTES as usize <= got && at + (i as u64) < limit {
            let h = &buf[i..i + HDR_BYTES as usize];
            if h[0] == MAGIC[0] && header_plausible(h) && at + i as u64 + u64_at(h, 24) == ixoff_abs
            {
                return Ok(Some(at + i as u64));
            }
            i += 1;
        }
        at += CHUNK as u64;
    }
    Ok(None)
}

pub fn open(storage: &Arc<Storage>, tail: Option<Arc<Tail>>) -> Result<Opened> {
    let mut r = storage.reader(tail);
    let len = storage.original_len();
    let base = locate(&mut r, len)
        .context("reading the FreeAce archive")?
        .ok_or_else(|| Damaged(anyhow!("the FreeAce archive header or index is unreadable")))?;
    let mut header = [0u8; HDR_BYTES as usize];
    r.seek(SeekFrom::Start(base))?;
    r.read_exact(&mut header)
        .context("reading the FreeAce header")?;
    let idxoff = u64_at(&header, 24);

    let mut trailer = [0u8; TRL_BYTES as usize];
    r.seek(SeekFrom::Start(len - TRL_BYTES))?;
    r.read_exact(&mut trailer)
        .context("reading the FreeAce trailer")?;
    let ixh = u64_at(&trailer, 0);
    let ixn = u64_at(&trailer, 8);
    if ixn > INDEX_MAX {
        return Err(Damaged(anyhow!("the FreeAce archive index is unreadable")).into());
    }
    // Offsets inside the archive are relative to its header; the file adds
    // `base` for an SFX stub in front.
    let ixoff_abs = len - TRL_BYTES - ixn;
    let ixoff = ixoff_abs - base;
    // The decoder reopens a shortened archive from the header's copy of this
    // offset, so it must agree with the trailer before anything is consumed.
    if idxoff != ixoff {
        bail!(
            "this FreeAce archive does not record its index position in its header, so it \
             cannot be extracted in place. Extract it normally"
        );
    }
    let mut tail_bytes = vec![0u8; (ixn + TRL_BYTES) as usize];
    r.seek(SeekFrom::Start(ixoff_abs))?;
    r.read_exact(&mut tail_bytes)
        .context("reading the FreeAce index")?;
    if xxh64(&tail_bytes[..ixn as usize]) != ixh {
        return Err(Damaged(anyhow!("the FreeAce archive index is corrupt")).into());
    }
    let (members, segs) = parse_index(&tail_bytes[..ixn as usize], ixoff)?;
    let (mut units, names) = build_units(&members, &segs, ixoff)?;
    // The lowest unit stays at 0 so it takes the stub along with the header.
    for unit in units.iter_mut().filter(|u| u.start > 0) {
        unit.start += base;
    }

    Ok(Opened {
        name: NAME,
        format: Box::new(Freeace {
            storage: storage.clone(),
            tail_bytes: Arc::new(tail_bytes),
            names,
            base,
        }),
        units,
        tail_start: ixoff_abs,
    })
}

fn parse_index(ix: &[u8], ixoff: u64) -> Result<(Vec<Member>, Vec<Seg>)> {
    let corrupt = || Damaged(anyhow!("the FreeAce archive index is corrupt"));
    let mut at = 0usize;
    let mut take = |n: usize| -> Result<&[u8]> {
        let bytes = ix.get(at..at + n).ok_or_else(corrupt)?;
        at += n;
        Ok(bytes)
    };
    let nm = u64_at(take(8)?, 0);
    if nm > 1 << 24 {
        return Err(corrupt().into());
    }
    let mut members = Vec::with_capacity(nm as usize);
    for _ in 0..nm {
        let nl = u16_at(take(2)?, 0) as usize;
        let name = String::from_utf8(take(nl)?.to_vec())
            .map_err(|_| anyhow!("a FreeAce member name is not valid UTF-8"))?;
        let size = u64_at(take(8)?, 0);
        let mtime = u64_at(take(8)?, 0) as i64;
        let mode = u32::from_le_bytes(take(4)?.try_into().unwrap());
        take(32)?; // SHA-256, verified by the decoder
        let seg0 = u64_at(take(8)?, 0);
        let nseg = u64_at(take(8)?, 0);
        members.push(Member {
            name,
            size,
            mtime,
            mode,
            seg0,
            nseg,
        });
    }
    let ns = u64_at(take(8)?, 0);
    if ns > 1 << 28 {
        return Err(corrupt().into());
    }
    let mut segs = Vec::with_capacity(ns as usize);
    for _ in 0..ns {
        let rec = take(40)?;
        let (off, clen) = (u64_at(rec, 0), u64_at(rec, 8));
        if off < HDR_BYTES || clen > ixoff || off > ixoff - clen {
            return Err(corrupt().into());
        }
        segs.push(Seg { off, clen });
    }
    // Recovery records may follow; they are not needed once the archive has
    // passed a full scrub, which the front end runs before consuming it.
    for m in &members {
        if m.seg0 > ns || m.nseg > ns - m.seg0 {
            return Err(corrupt().into());
        }
    }
    Ok((members, segs))
}

/// One unit per distinct segment run, holding every member that decodes
/// from it. The lowest unit starts at 0 so the header goes with it, and
/// empty members (no segments) ride along there.
fn build_units(
    members: &[Member],
    segs: &[Seg],
    ixoff: u64,
) -> Result<(Vec<Unit>, Vec<Vec<String>>)> {
    let mut runs: BTreeMap<u64, (u64, Vec<&Member>)> = BTreeMap::new();
    let mut empty = Vec::new();
    for m in members {
        if m.nseg == 0 {
            empty.push(m);
            continue;
        }
        let run = runs.entry(m.seg0).or_insert((m.nseg, Vec::new()));
        if run.0 != m.nseg {
            bail!(
                "members of this FreeAce archive share data in a way in-place extraction \
                 cannot split safely. Extract it normally"
            );
        }
        run.1.push(m);
    }
    let mut spans: Vec<(u64, u64, Vec<&Member>)> = runs
        .into_iter()
        .map(|(seg0, (nseg, ms))| {
            let run = &segs[seg0 as usize..(seg0 + nseg) as usize];
            let start = run.iter().map(|s| s.off).min().unwrap();
            let end = run.iter().map(|s| s.off + s.clen).max().unwrap();
            (start, end, ms)
        })
        .collect();
    spans.sort_by_key(|s| s.0);
    for pair in spans.windows(2) {
        if pair[0].1 > pair[1].0 {
            bail!(
                "this FreeAce archive interleaves its members' data, so it cannot be \
                 extracted in place. Extract it normally"
            );
        }
    }
    if spans.last().is_some_and(|s| s.1 > ixoff) {
        return Err(Damaged(anyhow!("the FreeAce archive index is corrupt")).into());
    }
    if spans.is_empty() {
        spans.push((0, 0, Vec::new()));
    }
    spans[0].0 = 0;
    spans[0].2.extend(empty);

    let mut units = Vec::with_capacity(spans.len());
    let mut names = Vec::with_capacity(spans.len());
    for (key, (start, _, ms)) in spans.into_iter().enumerate() {
        let mut items = Vec::with_capacity(ms.len());
        let mut unit_names = Vec::with_capacity(ms.len());
        for m in ms {
            let path = safe_path(&m.name).ok_or_else(|| {
                anyhow!(
                    "'{}' would be written outside the destination folder; extraction refused",
                    m.name
                )
            })?;
            items.push(Item {
                path,
                size: m.size,
                kind: Kind::File,
                mode: (m.mode & 0o7777 != 0).then_some(m.mode & 0o7777),
                mtime: Some(if m.mtime >= 0 {
                    UNIX_EPOCH + Duration::from_secs(m.mtime as u64)
                } else {
                    UNIX_EPOCH - Duration::from_secs(m.mtime.unsigned_abs())
                }),
            });
            unit_names.push(m.name.clone());
        }
        units.push(Unit {
            start,
            key,
            items,
            encrypted: false,
        });
        names.push(unit_names);
    }
    Ok((units, names))
}

impl Format for Freeace {
    fn worker(&self, _threads: u32) -> Result<Box<dyn Worker + '_>> {
        let decoder = DECODER
            .get()
            .cloned()
            .ok_or_else(|| anyhow!("the FreeAce decoder is not available in this build"))?;
        let mut index = tempfile::Builder::new()
            .prefix("freeace-index-")
            .tempfile()
            .context("saving the FreeAce index for the decoder")?;
        io::Write::write_all(&mut index, &self.tail_bytes)?;
        let index = index.into_temp_path();
        Ok(Box::new(FreeaceWorker {
            fmt: self,
            decoder,
            index,
        }))
    }

    /// Each decode already uses every core (Gleipnir caps the count to fit
    /// in memory), and a -9 model costs about a gigabyte per thread, so two
    /// decoders at once would only compete for RAM.
    fn serial(&self) -> bool {
        true
    }
}

struct FreeaceWorker<'a> {
    fmt: &'a Freeace,
    decoder: PathBuf,
    index: tempfile::TempPath,
}

impl Worker for FreeaceWorker<'_> {
    fn extract(&mut self, unit: &Unit, sink: &mut dyn Sink) -> Result<()> {
        for (item, name) in unit.items.iter().zip(&self.fmt.names[unit.key]) {
            let mut data = Decoded::spawn(
                &self.decoder,
                &self.index,
                self.fmt.storage.primary(),
                self.fmt.base,
                name,
            )?;
            sink.item(item, &mut data)?;
        }
        Ok(())
    }
}

/// A member streaming out of the decoder. End of stream is only reported
/// once the decoder has exited cleanly, which is when it has verified the
/// member; dropping it early kills the decoder.
struct Decoded {
    name: String,
    child: Child,
    stdout: ChildStdout,
    stderr: Option<JoinHandle<String>>,
    done: bool,
}

impl Decoded {
    fn spawn(
        decoder: &std::path::Path,
        index: &std::path::Path,
        archive: &std::path::Path,
        base: u64,
        name: &str,
    ) -> Result<Self> {
        let mut cmd = Command::new(decoder);
        cmd.args(["x", "-q", "-t0", "-O", "-I"]).arg(index);
        // With the index cut off, the decoder cannot find an SFX payload by
        // scanning, so it is told where the archive starts.
        if base > 0 {
            cmd.arg(format!("-B{base}"));
        }
        cmd.arg(archive)
            .arg(name)
            .stdin(Stdio::null())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped());
        #[cfg(windows)]
        {
            use std::os::windows::process::CommandExt;
            const CREATE_NO_WINDOW: u32 = 0x0800_0000;
            cmd.creation_flags(CREATE_NO_WINDOW);
        }
        let mut child = cmd
            .spawn()
            .with_context(|| format!("starting the FreeAce decoder for '{name}'"))?;
        let stdout = child.stdout.take().expect("piped stdout");
        let mut err = child.stderr.take().expect("piped stderr");
        let stderr = std::thread::spawn(move || {
            let mut text = String::new();
            let _ = err.read_to_string(&mut text);
            text
        });
        Ok(Self {
            name: name.to_string(),
            child,
            stdout,
            stderr: Some(stderr),
            done: false,
        })
    }
}

impl Read for Decoded {
    fn read(&mut self, buf: &mut [u8]) -> io::Result<usize> {
        let n = self.stdout.read(buf)?;
        if n > 0 || self.done || buf.is_empty() {
            return Ok(n);
        }
        self.done = true;
        let status = self.child.wait()?;
        let detail = self
            .stderr
            .take()
            .and_then(|h| h.join().ok())
            .unwrap_or_default();
        if status.success() {
            return Ok(0);
        }
        Err(io::Error::other(format!(
            "'{}' failed verification ({}): {}",
            self.name,
            status,
            detail.trim()
        )))
    }
}

impl Drop for Decoded {
    fn drop(&mut self) {
        if !self.done {
            let _ = self.child.kill();
            let _ = self.child.wait();
        }
    }
}

fn u16_at(b: &[u8], at: usize) -> u16 {
    u16::from_le_bytes(b[at..at + 2].try_into().unwrap())
}

fn u64_at(b: &[u8], at: usize) -> u64 {
    u64::from_le_bytes(b[at..at + 8].try_into().unwrap())
}

/// XXH64 with seed 0, as Gleipnir uses for its header and index checksums.
fn xxh64(input: &[u8]) -> u64 {
    const P1: u64 = 0x9E37_79B1_85EB_CA87;
    const P2: u64 = 0xC2B2_AE3D_27D4_EB4F;
    const P3: u64 = 0x1656_67B1_9E37_79F9;
    const P4: u64 = 0x85EB_CA77_C2B2_AE63;
    const P5: u64 = 0x27D4_EB2F_1656_67C5;
    fn round(acc: u64, lane: u64) -> u64 {
        acc.wrapping_add(lane.wrapping_mul(P2))
            .rotate_left(31)
            .wrapping_mul(P1)
    }
    fn merge(acc: u64, v: u64) -> u64 {
        (acc ^ round(0, v)).wrapping_mul(P1).wrapping_add(P4)
    }
    let lane = |b: &[u8]| u64::from_le_bytes(b[..8].try_into().unwrap());
    let mut p = input;
    let mut h = if input.len() >= 32 {
        let mut v = [P1.wrapping_add(P2), P2, 0, 0u64.wrapping_sub(P1)];
        while p.len() >= 32 {
            for (i, acc) in v.iter_mut().enumerate() {
                *acc = round(*acc, lane(&p[i * 8..]));
            }
            p = &p[32..];
        }
        let mut h = v[0]
            .rotate_left(1)
            .wrapping_add(v[1].rotate_left(7))
            .wrapping_add(v[2].rotate_left(12))
            .wrapping_add(v[3].rotate_left(18));
        for acc in v {
            h = merge(h, acc);
        }
        h
    } else {
        P5
    };
    h = h.wrapping_add(input.len() as u64);
    while p.len() >= 8 {
        h = (h ^ round(0, lane(p)))
            .rotate_left(27)
            .wrapping_mul(P1)
            .wrapping_add(P4);
        p = &p[8..];
    }
    if p.len() >= 4 {
        let word = u32::from_le_bytes(p[..4].try_into().unwrap()) as u64;
        h = (h ^ word.wrapping_mul(P1))
            .rotate_left(23)
            .wrapping_mul(P2)
            .wrapping_add(P3);
        p = &p[4..];
    }
    for &byte in p {
        h = (h ^ (byte as u64).wrapping_mul(P5))
            .rotate_left(11)
            .wrapping_mul(P1);
    }
    h ^= h >> 33;
    h = h.wrapping_mul(P2);
    h ^= h >> 29;
    h = h.wrapping_mul(P3);
    h ^ (h >> 32)
}

#[cfg(test)]
mod tests {
    use super::xxh64;

    #[test]
    fn xxh64_matches_reference_vectors() {
        assert_eq!(xxh64(b""), 0xEF46_DB37_51D8_E999);
        assert_eq!(xxh64(b"a"), 0xD24E_C4F1_A98C_6E5B);
        assert_eq!(
            xxh64(b"Nobody inspects the spammish repetition"),
            0xFBCE_A83C_8A37_8BF1
        );
    }
}
