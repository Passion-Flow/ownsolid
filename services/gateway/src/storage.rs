use crate::configuration::{Configuration, Result, private_directory, secret_file};
use crate::outcome::Outcome;
use nix::fcntl::{Flock, FlockArg, OFlag};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::collections::{BTreeMap, HashSet};
use std::fs::{self, File, OpenOptions};
use std::io::Write;
use std::os::unix::fs::{DirBuilderExt, MetadataExt, OpenOptionsExt, PermissionsExt};
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use uuid::Uuid;

mod fence;
pub use fence::{FenceReceipt, validate_receipt_path, write_receipt};

const RECORD_VERSION: u32 = 1;
const MAX_RECORD_BYTES: u64 = 4_096;
const MAX_STARTUP_ENTRIES: usize = 65_536;

#[derive(Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct Owner {
    schema_version: u32,
    gateway_id: Uuid,
    node_id: Uuid,
    control_origin_sha256: String,
}

#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct Record {
    owner: Owner,
    outcome: Outcome,
    canonical_sha256: String,
}

struct State {
    pending: BTreeMap<Uuid, (String, u64)>,
    reserved: HashSet<Uuid>,
    bytes: u64,
    healthy: bool,
    recovery_required: bool,
    fenced: bool,
}

pub struct Spool {
    root: PathBuf,
    admission_allowed: bool,
    directory: PathBuf,
    directory_device: u64,
    directory_inode: u64,
    owner: Owner,
    maximum_entries: usize,
    maximum_bytes: u64,
    minimum_free_bytes: u64,
    state: Mutex<State>,
    _lock: Flock<File>,
}

pub struct Reservation {
    spool: Arc<Spool>,
    request_id: Uuid,
}

fn sync_directory(path: &Path) -> Result<()> {
    File::open(path)
        .and_then(|file| file.sync_all())
        .map_err(|_| "GATEWAY_SPOOL_SYNC_FAILED")
}

fn new_private_file(path: &Path) -> Result<File> {
    OpenOptions::new()
        .write(true)
        .read(true)
        .create_new(true)
        .mode(0o600)
        .custom_flags(OFlag::O_NOFOLLOW.bits())
        .open(path)
        .map_err(|_| "GATEWAY_SPOOL_CREATE_FAILED")
}

fn byte_product<A: Into<u64>, B: Into<u64>>(blocks: A, size: B) -> u64 {
    blocks.into().saturating_mul(size.into())
}

pub(crate) fn filesystem_free_bytes(path: &Path) -> Result<u64> {
    nix::sys::statvfs::statvfs(path)
        .map(|stat| byte_product(stat.blocks_available(), stat.fragment_size()))
        .map_err(|_| "GATEWAY_SPOOL_SPACE_UNAVAILABLE")
}

impl Spool {
    pub fn open(configuration: &Configuration) -> Result<Arc<Self>> {
        Self::open_mode(configuration, true)
    }

    /// Offline operator access cannot reserve capacity or execute a new inference.
    pub fn open_for_delivery(configuration: &Configuration) -> Result<Arc<Self>> {
        Self::open_mode(configuration, false)
    }

    fn open_mode(configuration: &Configuration, admission_allowed: bool) -> Result<Arc<Self>> {
        configuration.validate()?;
        let root = &configuration.state_directory;
        private_directory(root)?;
        let owner = Owner {
            schema_version: RECORD_VERSION,
            gateway_id: configuration.gateway_id,
            node_id: configuration.node_id,
            control_origin_sha256: Sha256::digest(configuration.control_origin.as_bytes())
                .iter()
                .map(|byte| format!("{byte:02x}"))
                .collect(),
        };
        let marker = root.join("owner.json");
        if !marker.try_exists().map_err(|_| "GATEWAY_SPOOL_IO_FAILED")? {
            if !admission_allowed {
                return Err("GATEWAY_STATE_NOT_INITIALIZED");
            }
            if fs::read_dir(root)
                .map_err(|_| "GATEWAY_SPOOL_IO_FAILED")?
                .next()
                .is_some()
            {
                return Err("GATEWAY_STATE_NOT_EMPTY");
            }
            let mut file = new_private_file(&marker)?;
            file.write_all(&serde_json::to_vec(&owner).map_err(|_| "GATEWAY_SPOOL_IO_FAILED")?)
                .and_then(|_| file.sync_all())
                .map_err(|_| "GATEWAY_SPOOL_SYNC_FAILED")?;
            sync_directory(root)?;
        }
        let actual: Owner = serde_json::from_slice(&secret_file(&marker)?)
            .map_err(|_| "GATEWAY_STATE_OWNER_INVALID")?;
        if actual != owner {
            return Err("GATEWAY_STATE_OWNER_CONFLICT");
        }
        let lock_path = root.join("spool.lock");
        let lock = OpenOptions::new()
            .read(true)
            .write(true)
            .create(true)
            .truncate(false)
            .mode(0o600)
            .custom_flags(OFlag::O_NOFOLLOW.bits())
            .open(&lock_path)
            .map_err(|_| "GATEWAY_STATE_LOCK_INVALID")?;
        let metadata = lock.metadata().map_err(|_| "GATEWAY_STATE_LOCK_INVALID")?;
        if !metadata.is_file()
            || metadata.uid() != nix::unistd::geteuid().as_raw()
            || metadata.mode() & 0o077 != 0
            || metadata.len() != 0
        {
            return Err("GATEWAY_STATE_LOCK_INVALID");
        }
        let lock = Flock::lock(lock, FlockArg::LockExclusiveNonblock)
            .map_err(|_| "GATEWAY_STATE_LOCKED")?;
        let directory = root.join("outcomes");
        if !directory
            .try_exists()
            .map_err(|_| "GATEWAY_SPOOL_IO_FAILED")?
        {
            fs::DirBuilder::new()
                .mode(0o700)
                .create(&directory)
                .map_err(|_| "GATEWAY_SPOOL_CREATE_FAILED")?;
            sync_directory(root)?;
        }
        private_directory(&directory)?;
        for entry in fs::read_dir(root).map_err(|_| "GATEWAY_SPOOL_IO_FAILED")? {
            let entry = entry.map_err(|_| "GATEWAY_SPOOL_IO_FAILED")?;
            if !matches!(
                entry.file_name().to_str(),
                Some("owner.json" | "spool.lock" | "outcomes" | "fence.json" | "fence.stage.json")
            ) {
                return Err("GATEWAY_STATE_UNKNOWN_ENTRY");
            }
        }
        let fenced = fence::recover(root, &owner)?.is_some();
        if admission_allowed && fenced {
            return Err("GATEWAY_STATE_FENCED");
        }
        let directory_metadata = fs::metadata(&directory).map_err(|_| "GATEWAY_SPOOL_IO_FAILED")?;
        let mut spool = Self {
            root: root.clone(),
            admission_allowed,
            directory_device: directory_metadata.dev(),
            directory_inode: directory_metadata.ino(),
            directory,
            owner,
            maximum_entries: configuration.limits.spool_max_entries,
            maximum_bytes: configuration.limits.spool_max_bytes,
            minimum_free_bytes: configuration.limits.minimum_free_disk_bytes,
            state: Mutex::new(State {
                pending: BTreeMap::new(),
                reserved: HashSet::new(),
                bytes: 0,
                healthy: true,
                recovery_required: false,
                fenced,
            }),
            _lock: lock,
        };
        spool.recover()?;
        let state = spool
            .state
            .get_mut()
            .map_err(|_| "GATEWAY_SPOOL_STATE_INVALID")?;
        state.recovery_required = !state.pending.is_empty();
        Ok(Arc::new(spool))
    }

    fn owned_directory(&self) -> Result<()> {
        private_directory(&self.directory).map_err(|_| "GATEWAY_SPOOL_LOCATION_CHANGED")?;
        let metadata =
            fs::symlink_metadata(&self.directory).map_err(|_| "GATEWAY_SPOOL_LOCATION_CHANGED")?;
        if metadata.dev() != self.directory_device || metadata.ino() != self.directory_inode {
            return Err("GATEWAY_SPOOL_LOCATION_CHANGED");
        }
        Ok(())
    }

    fn path(&self, request_id: Uuid) -> PathBuf {
        self.directory.join(format!("{request_id}.json"))
    }

    fn record(&self, path: &Path) -> Result<Record> {
        let bytes = secret_file(path)?;
        if bytes.len() as u64 > MAX_RECORD_BYTES {
            return Err("GATEWAY_SPOOL_RECORD_INVALID");
        }
        let record: Record =
            serde_json::from_slice(&bytes).map_err(|_| "GATEWAY_SPOOL_RECORD_INVALID")?;
        if record.owner != self.owner || record.outcome.digest()? != record.canonical_sha256 {
            return Err("GATEWAY_SPOOL_RECORD_INVALID");
        }
        Ok(record)
    }

    fn recover(&mut self) -> Result<()> {
        let mut stages = Vec::new();
        for entry in fs::read_dir(&self.directory).map_err(|_| "GATEWAY_SPOOL_IO_FAILED")? {
            let entry = entry.map_err(|_| "GATEWAY_SPOOL_IO_FAILED")?;
            let name = entry.file_name();
            let name = name.to_str().ok_or("GATEWAY_SPOOL_UNKNOWN_ENTRY")?;
            let stage = name.starts_with(".stage-");
            let value = name
                .strip_prefix(".stage-")
                .unwrap_or(name)
                .strip_suffix(".json")
                .ok_or("GATEWAY_SPOOL_UNKNOWN_ENTRY")?;
            let id = Uuid::parse_str(value).map_err(|_| "GATEWAY_SPOOL_UNKNOWN_ENTRY")?;
            if id.to_string() != value {
                return Err("GATEWAY_SPOOL_UNKNOWN_ENTRY");
            }
            let record = self.record(&entry.path())?;
            if record.outcome.request_id != id {
                return Err("GATEWAY_SPOOL_RECORD_INVALID");
            }
            if stage {
                stages.push(entry.path());
            } else {
                let state = self
                    .state
                    .get_mut()
                    .map_err(|_| "GATEWAY_SPOOL_STATE_INVALID")?;
                let bytes = entry
                    .metadata()
                    .map_err(|_| "GATEWAY_SPOOL_IO_FAILED")?
                    .len();
                state.pending.insert(id, (record.canonical_sha256, bytes));
                state.bytes += bytes;
            }
            if stages.len()
                + self
                    .state
                    .get_mut()
                    .map_err(|_| "GATEWAY_SPOOL_STATE_INVALID")?
                    .pending
                    .len()
                > MAX_STARTUP_ENTRIES * 2
            {
                return Err("GATEWAY_SPOOL_CAPACITY_EXCEEDED");
            }
        }
        for stage in stages {
            let record = self.record(&stage)?;
            let final_path = self.path(record.outcome.request_id);
            if final_path
                .try_exists()
                .map_err(|_| "GATEWAY_SPOOL_IO_FAILED")?
            {
                if self.record(&final_path)?.canonical_sha256 != record.canonical_sha256 {
                    return Err("GATEWAY_SPOOL_RECORD_CONFLICT");
                }
            } else {
                fs::hard_link(&stage, &final_path).map_err(|_| "GATEWAY_SPOOL_PUBLISH_FAILED")?;
                sync_directory(&self.directory)?;
                let bytes = final_path
                    .metadata()
                    .map_err(|_| "GATEWAY_SPOOL_IO_FAILED")?
                    .len();
                let state = self
                    .state
                    .get_mut()
                    .map_err(|_| "GATEWAY_SPOOL_STATE_INVALID")?;
                state
                    .pending
                    .insert(record.outcome.request_id, (record.canonical_sha256, bytes));
                state.bytes += bytes;
            }
            fs::remove_file(stage).map_err(|_| "GATEWAY_SPOOL_IO_FAILED")?;
            sync_directory(&self.directory)?;
        }
        Ok(())
    }

    fn disk_available(&self) -> bool {
        filesystem_free_bytes(&self.directory).is_ok_and(|free| free >= self.minimum_free_bytes)
    }

    pub fn ready(&self) -> bool {
        self.admission_allowed
            && self.owned_directory().is_ok()
            && self.state.lock().is_ok_and(|state| {
                state.healthy
                    && !state.fenced
                    && !state.recovery_required
                    && state.pending.len() + state.reserved.len() < self.maximum_entries
                    && state.bytes + (state.reserved.len() as u64 + 1) * MAX_RECORD_BYTES
                        <= self.maximum_bytes
            })
            && self.disk_available()
    }

    pub fn degrade(&self) {
        if let Ok(mut state) = self.state.lock() {
            state.healthy = false;
        }
    }

    pub fn reserve(self: &Arc<Self>, request_id: Uuid) -> Result<Reservation> {
        self.owned_directory()
            .map_err(|_| "usage_storage_unavailable")?;
        let mut state = self.state.lock().map_err(|_| "usage_storage_unavailable")?;
        if request_id.is_nil()
            || !self.admission_allowed
            || state.fenced
            || !state.healthy
            || state.recovery_required
            || state.pending.contains_key(&request_id)
            || state.reserved.contains(&request_id)
            || state.pending.len() + state.reserved.len() >= self.maximum_entries
            || state.bytes + (state.reserved.len() as u64 + 1) * MAX_RECORD_BYTES
                > self.maximum_bytes
            || !self.disk_available()
        {
            return Err("usage_storage_unavailable");
        }
        state.reserved.insert(request_id);
        Ok(Reservation {
            spool: self.clone(),
            request_id,
        })
    }

    fn persist(&self, outcome: &Outcome) -> Result<()> {
        let mut state = self
            .state
            .lock()
            .map_err(|_| "GATEWAY_SPOOL_STATE_INVALID")?;
        let result = (|| {
            self.owned_directory()?;
            let digest = outcome.digest()?;
            if let Some((existing, _)) = state.pending.get(&outcome.request_id) {
                return if existing == &digest {
                    Ok(())
                } else {
                    Err("GATEWAY_SPOOL_RECORD_CONFLICT")
                };
            }
            let record = Record {
                owner: self.owner.clone(),
                outcome: outcome.clone(),
                canonical_sha256: digest.clone(),
            };
            let bytes = serde_json::to_vec(&record).map_err(|_| "GATEWAY_SPOOL_RECORD_INVALID")?;
            if bytes.len() as u64 > MAX_RECORD_BYTES {
                return Err("GATEWAY_SPOOL_RECORD_INVALID");
            }
            let stage = self
                .directory
                .join(format!(".stage-{}.json", outcome.request_id));
            let final_path = self.path(outcome.request_id);
            let mut file = new_private_file(&stage)?;
            file.write_all(&bytes)
                .map_err(|_| "GATEWAY_SPOOL_IO_FAILED")?;
            file.set_permissions(fs::Permissions::from_mode(0o400))
                .and_then(|_| file.sync_all())
                .map_err(|_| "GATEWAY_SPOOL_SYNC_FAILED")?;
            fs::hard_link(&stage, &final_path).map_err(|_| "GATEWAY_SPOOL_PUBLISH_FAILED")?;
            sync_directory(&self.directory)?;
            fs::remove_file(stage).map_err(|_| "GATEWAY_SPOOL_IO_FAILED")?;
            sync_directory(&self.directory)?;
            state
                .pending
                .insert(outcome.request_id, (digest, bytes.len() as u64));
            state.bytes += bytes.len() as u64;
            Ok(())
        })();
        if result.is_err() {
            state.healthy = false
        }
        result
    }

    pub fn next_pending(&self) -> Result<Option<Outcome>> {
        self.owned_directory()?;
        let mut state = self
            .state
            .lock()
            .map_err(|_| "GATEWAY_SPOOL_STATE_INVALID")?;
        let Some((id, (expected, _))) = state.pending.first_key_value() else {
            return Ok(None);
        };
        let id = *id;
        let expected = expected.clone();
        let record = match self.record(&self.path(id)) {
            Ok(record) => record,
            Err(error) => {
                state.healthy = false;
                return Err(error);
            }
        };
        if record.canonical_sha256 != expected {
            state.healthy = false;
            return Err("GATEWAY_SPOOL_RECORD_CONFLICT");
        }
        Ok(Some(record.outcome))
    }

    /// Called only after Control::deliver_outcome validates the matching durable server receipt.
    pub fn acknowledge(&self, outcome: &Outcome) -> Result<()> {
        self.owned_directory()?;
        outcome.validate()?;
        let mut state = self
            .state
            .lock()
            .map_err(|_| "GATEWAY_SPOOL_STATE_INVALID")?;
        let Some((digest, bytes)) = state.pending.get(&outcome.request_id).cloned() else {
            return Ok(());
        };
        if digest != outcome.digest()?
            || self
                .record(&self.path(outcome.request_id))?
                .canonical_sha256
                != digest
        {
            return Err("GATEWAY_SPOOL_RECORD_CONFLICT");
        }
        if fs::remove_file(self.path(outcome.request_id)).is_err()
            || sync_directory(&self.directory).is_err()
        {
            state.healthy = false;
            return Err("GATEWAY_SPOOL_SYNC_FAILED");
        }
        state.pending.remove(&outcome.request_id);
        state.bytes -= bytes;
        if state.pending.is_empty() {
            state.recovery_required = false;
        }
        Ok(())
    }
}

impl Reservation {
    pub fn complete(self, outcome: &Outcome) -> Result<()> {
        if self.request_id != outcome.request_id {
            return Err("GATEWAY_SPOOL_RECORD_CONFLICT");
        }
        self.spool.persist(outcome)
    }
}

impl Drop for Reservation {
    fn drop(&mut self) {
        if let Ok(mut state) = self.spool.state.lock() {
            state.reserved.remove(&self.request_id);
        }
    }
}
