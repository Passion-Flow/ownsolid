use super::{Owner, Spool, new_private_file, sync_directory};
use crate::configuration::{Result, private_directory, secret_file};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::fs;
use std::io::Write;
use std::os::unix::fs::PermissionsExt;
use std::path::Path;
use std::time::{SystemTime, UNIX_EPOCH};
use uuid::Uuid;

const FENCE_SCHEMA_VERSION: u32 = 1;
const MAX_FENCE_BYTES: usize = 4096;
const MAX_JSON_INTEGER: u64 = 9_007_199_254_740_991;

#[derive(Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub(super) struct Marker {
    schema_version: u32,
    owner: Owner,
    fenced_at_unix_milliseconds: u64,
}

#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct FenceReceipt {
    pub schema_version: u32,
    pub gateway_id: Uuid,
    pub node_id: Uuid,
    pub control_origin_sha256: String,
    pub challenge_id: Uuid,
    pub state_fence_sha256: String,
    pub fenced_at_unix_milliseconds: u64,
    pub checked_at_unix_milliseconds: u64,
    pub exclusive_state_lock_confirmed: bool,
    pub persistent_fence_confirmed: bool,
    pub pending_outcomes: usize,
    pub gateway_version: String,
}

fn digest(bytes: &[u8]) -> String {
    Sha256::digest(bytes)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}
fn now() -> Result<u64> {
    let value: u64 = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_err(|_| "GATEWAY_CLOCK_INVALID")?
        .as_millis()
        .try_into()
        .map_err(|_| "GATEWAY_CLOCK_INVALID")?;
    if value == 0 || value > MAX_JSON_INTEGER {
        return Err("GATEWAY_CLOCK_INVALID");
    }
    Ok(value)
}
fn present(path: &Path) -> Result<bool> {
    match fs::symlink_metadata(path) {
        Ok(_) => Ok(true),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(false),
        Err(_) => Err("GATEWAY_FENCE_IO_FAILED"),
    }
}
fn read(path: &Path, owner: &Owner) -> Result<Marker> {
    let bytes = secret_file(path)?;
    if bytes.len() > MAX_FENCE_BYTES {
        return Err("GATEWAY_FENCE_RECORD_INVALID");
    }
    let value: Marker =
        serde_json::from_slice(&bytes).map_err(|_| "GATEWAY_FENCE_RECORD_INVALID")?;
    if value.schema_version != FENCE_SCHEMA_VERSION
        || value.owner != *owner
        || value.fenced_at_unix_milliseconds == 0
        || value.fenced_at_unix_milliseconds > MAX_JSON_INTEGER
        || serde_json::to_vec(&value).map_err(|_| "GATEWAY_FENCE_RECORD_INVALID")? != bytes
    {
        return Err("GATEWAY_FENCE_RECORD_INVALID");
    }
    Ok(value)
}

pub(super) fn recover(root: &Path, owner: &Owner) -> Result<Option<Marker>> {
    private_directory(root)?;
    let stage = root.join("fence.stage.json");
    let target = root.join("fence.json");
    if present(&stage)? {
        let staged = read(&stage, owner)?;
        if present(&target)? {
            if read(&target, owner)? != staged {
                return Err("GATEWAY_FENCE_RECORD_CONFLICT");
            }
        } else {
            fs::set_permissions(&stage, fs::Permissions::from_mode(0o400))
                .map_err(|_| "GATEWAY_FENCE_IO_FAILED")?;
            fs::File::open(&stage)
                .and_then(|file| file.sync_all())
                .map_err(|_| "GATEWAY_FENCE_SYNC_FAILED")?;
            fs::hard_link(&stage, &target).map_err(|_| "GATEWAY_FENCE_PUBLISH_FAILED")?;
        }
        sync_directory(root)?;
        fs::remove_file(&stage).map_err(|_| "GATEWAY_FENCE_IO_FAILED")?;
        sync_directory(root)?;
    }
    if present(&target)? {
        Ok(Some(read(&target, owner)?))
    } else {
        Ok(None)
    }
}

impl Spool {
    /// The caller holds this exact original spool's exclusive lifetime file lock.
    pub fn fence(&self, challenge_id: Uuid) -> Result<FenceReceipt> {
        if self.admission_allowed || challenge_id.is_nil() {
            return Err("GATEWAY_OPERATOR_MODE_REQUIRED");
        }
        self.owned_directory()?;
        private_directory(&self.root)?;
        let actual: Owner = serde_json::from_slice(&secret_file(&self.root.join("owner.json"))?)
            .map_err(|_| "GATEWAY_STATE_OWNER_INVALID")?;
        if actual != self.owner {
            return Err("GATEWAY_STATE_OWNER_CONFLICT");
        }
        let mut state = self
            .state
            .lock()
            .map_err(|_| "GATEWAY_SPOOL_STATE_INVALID")?;
        let marker = if let Some(existing) = recover(&self.root, &self.owner)? {
            existing
        } else {
            let marker = Marker {
                schema_version: FENCE_SCHEMA_VERSION,
                owner: self.owner.clone(),
                fenced_at_unix_milliseconds: now()?,
            };
            let bytes = serde_json::to_vec(&marker).map_err(|_| "GATEWAY_FENCE_RECORD_INVALID")?;
            let stage = self.root.join("fence.stage.json");
            let mut file = new_private_file(&stage)?;
            file.write_all(&bytes)
                .map_err(|_| "GATEWAY_FENCE_IO_FAILED")?;
            file.set_permissions(fs::Permissions::from_mode(0o400))
                .and_then(|_| file.sync_all())
                .map_err(|_| "GATEWAY_FENCE_SYNC_FAILED")?;
            fs::hard_link(&stage, self.root.join("fence.json"))
                .map_err(|_| "GATEWAY_FENCE_PUBLISH_FAILED")?;
            sync_directory(&self.root)?;
            fs::remove_file(stage).map_err(|_| "GATEWAY_FENCE_IO_FAILED")?;
            sync_directory(&self.root)?;
            marker
        };
        let confirmed = read(&self.root.join("fence.json"), &self.owner)?;
        if confirmed != marker {
            return Err("GATEWAY_FENCE_RECORD_CONFLICT");
        }
        state.fenced = true;
        let checked = now()?;
        if checked < marker.fenced_at_unix_milliseconds {
            return Err("GATEWAY_CLOCK_INVALID");
        }
        Ok(FenceReceipt {
            schema_version: FENCE_SCHEMA_VERSION,
            gateway_id: self.owner.gateway_id,
            node_id: self.owner.node_id,
            control_origin_sha256: self.owner.control_origin_sha256.clone(),
            challenge_id,
            state_fence_sha256: digest(
                &serde_json::to_vec(&marker).map_err(|_| "GATEWAY_FENCE_RECORD_INVALID")?,
            ),
            fenced_at_unix_milliseconds: marker.fenced_at_unix_milliseconds,
            checked_at_unix_milliseconds: checked,
            exclusive_state_lock_confirmed: true,
            persistent_fence_confirmed: true,
            pending_outcomes: state.pending.len(),
            gateway_version: env!("CARGO_PKG_VERSION").into(),
        })
    }

    pub fn inspection(&self) -> Result<serde_json::Value> {
        self.owned_directory()?;
        let state = self
            .state
            .lock()
            .map_err(|_| "GATEWAY_SPOOL_STATE_INVALID")?;
        Ok(
            serde_json::json!({"gateway_id":self.owner.gateway_id,"node_id":self.owner.node_id,"fenced":state.fenced,"delivery_only":!self.admission_allowed,"pending_outcomes":state.pending.len(),"reserved_outcomes":state.reserved.len(),"pending_bytes":state.bytes,"integrity_healthy":state.healthy,"startup_recovery_required":state.recovery_required}),
        )
    }
}

pub fn validate_receipt_path(path: &Path) -> Result<()> {
    if !path.is_absolute()
        || path
            .components()
            .any(|component| matches!(component, std::path::Component::ParentDir))
    {
        return Err("GATEWAY_RECEIPT_PATH_INVALID");
    }
    let parent = path.parent().ok_or("GATEWAY_RECEIPT_PATH_INVALID")?;
    private_directory(parent)?;
    if present(path)? {
        return Err("GATEWAY_RECEIPT_ALREADY_EXISTS");
    }
    Ok(())
}

pub fn write_receipt(path: &Path, receipt: &FenceReceipt) -> Result<()> {
    validate_receipt_path(path)?;
    let parent = path.parent().ok_or("GATEWAY_RECEIPT_PATH_INVALID")?;
    let bytes = serde_json::to_vec(receipt).map_err(|_| "GATEWAY_FENCE_RECORD_INVALID")?;
    if bytes.len() > MAX_FENCE_BYTES {
        return Err("GATEWAY_FENCE_RECORD_INVALID");
    }
    let mut file = new_private_file(path)?;
    file.write_all(&bytes)
        .and_then(|_| file.sync_all())
        .map_err(|_| "GATEWAY_FENCE_SYNC_FAILED")?;
    sync_directory(parent)
}
