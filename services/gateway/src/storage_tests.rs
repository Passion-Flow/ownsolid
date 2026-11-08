//! Actual native filesystem/locking/reopen tests; no process-kill or Go persistence claim.
use crate::configuration::{Configuration, Limits};
use crate::outcome::{ErrorCode, Outcome, Status};
use crate::storage::Spool;
use std::fs;
use std::os::unix::fs::PermissionsExt;
use std::path::PathBuf;
use uuid::Uuid;

struct Fixture {
    root: PathBuf,
    configuration: Configuration,
}
impl Fixture {
    fn new() -> Self {
        let root = std::env::temp_dir().join(format!("gateway-spool-{}", Uuid::new_v4()));
        fs::create_dir(&root).unwrap();
        fs::set_permissions(&root, fs::Permissions::from_mode(0o700)).unwrap();
        let root = root.canonicalize().unwrap();
        let state = root.join("state");
        fs::create_dir(&state).unwrap();
        fs::set_permissions(&state, fs::Permissions::from_mode(0o700)).unwrap();
        let configuration = Configuration {
            quota_policy: None,
            gateway_id: Uuid::new_v4(),
            node_id: Uuid::new_v4(),
            listen_address: "127.0.0.1:19443".parse().unwrap(),
            backend_address: "127.0.0.1:19001".parse().unwrap(),
            control_origin: "https://admin.example.invalid:19444".into(),
            site_id: "default".to_owned(),
            cell_id: "default".to_owned(),
            control_ca_file: root.join("ca.pem"),
            gateway_credential_file: root.join("credential"),
            tls_certificate_file: root.join("certificate.pem"),
            tls_private_key_file: root.join("key.pem"),
            state_directory: state,
            limits: Limits {
                spool_max_entries: 2,
                spool_max_bytes: 8192,
                ..Limits::default()
            },
        };
        Self {
            root,
            configuration,
        }
    }
}
impl Drop for Fixture {
    fn drop(&mut self) {
        fs::remove_dir_all(&self.root).unwrap();
    }
}

fn outcome() -> Outcome {
    Outcome {
        request_id: Uuid::new_v4(),
        status: Status::Incomplete,
        error_code: Some(ErrorCode::GatewayInterrupted),
        usage: None,
        elapsed_milliseconds: 0,
        output_started: false,
    }
}

#[test]
fn actual_execution_drop_persists_unknown_usage_and_success_is_durable_before_return() {
    let fixture = Fixture::new();
    let spool = Spool::open(&fixture.configuration).unwrap();
    let id = Uuid::new_v4();
    {
        let mut execution = crate::admission::Execution::new(spool.reserve(id).unwrap(), id);
        execution.output_started();
    }
    let incomplete = spool.next_pending().unwrap().unwrap();
    assert_eq!(incomplete.request_id, id);
    assert_eq!(incomplete.status, Status::ClientCancelled);
    assert!(incomplete.output_started);
    assert!(incomplete.usage.is_none());
    spool.acknowledge(&incomplete).unwrap();
    let id = Uuid::new_v4();
    let mut execution = crate::admission::Execution::new(spool.reserve(id).unwrap(), id);
    execution
        .success(crate::protocol::Usage {
            prompt_tokens: 7,
            completion_tokens: 2,
            total_tokens: 9,
        })
        .unwrap();
    drop(execution);
    let success = spool.next_pending().unwrap().unwrap();
    assert_eq!(success.status, Status::Succeeded);
    assert_eq!(success.usage.unwrap().total_tokens, 9);
}

#[test]
fn actual_exclusive_operator_fence_is_persistent_and_delivery_mode_cannot_reserve() {
    let fixture = Fixture::new();
    assert!(matches!(
        Spool::open_for_delivery(&fixture.configuration),
        Err("GATEWAY_STATE_NOT_INITIALIZED")
    ));
    let live = Spool::open(&fixture.configuration).unwrap();
    assert!(matches!(
        Spool::open_for_delivery(&fixture.configuration),
        Err("GATEWAY_STATE_LOCKED")
    ));
    assert!(matches!(
        live.fence(Uuid::new_v4()),
        Err("GATEWAY_OPERATOR_MODE_REQUIRED")
    ));
    let value = outcome();
    live.reserve(value.request_id)
        .unwrap()
        .complete(&value)
        .unwrap();
    drop(live);
    let operator = Spool::open_for_delivery(&fixture.configuration).unwrap();
    assert!(!operator.ready());
    assert!(operator.reserve(Uuid::new_v4()).is_err());
    let challenge = Uuid::new_v4();
    let receipt = operator.fence(challenge).unwrap();
    assert_eq!(receipt.gateway_id, fixture.configuration.gateway_id);
    assert_eq!(receipt.node_id, fixture.configuration.node_id);
    assert_eq!(receipt.challenge_id, challenge);
    assert_eq!(receipt.pending_outcomes, 1);
    assert!(receipt.exclusive_state_lock_confirmed && receipt.persistent_fence_confirmed);
    let path = fixture.root.join("fence-receipt.json");
    crate::storage::write_receipt(&path, &receipt).unwrap();
    let original = fs::read(&path).unwrap();
    assert!(crate::storage::write_receipt(&path, &receipt).is_err());
    assert_eq!(fs::read(&path).unwrap(), original);
    let repeated = operator.fence(Uuid::new_v4()).unwrap();
    assert_eq!(repeated.state_fence_sha256, receipt.state_fence_sha256);
    assert_eq!(
        repeated.fenced_at_unix_milliseconds,
        receipt.fenced_at_unix_milliseconds
    );
    operator.acknowledge(&value).unwrap();
    assert!(operator.next_pending().unwrap().is_none());
    assert!(!operator.ready());
    drop(operator);
    assert!(matches!(
        Spool::open(&fixture.configuration),
        Err("GATEWAY_STATE_FENCED")
    ));
    let reopened = Spool::open_for_delivery(&fixture.configuration).unwrap();
    assert_eq!(reopened.inspection().unwrap()["fenced"], true);
    assert!(!reopened.ready());
    assert!(reopened.reserve(Uuid::new_v4()).is_err());
}

#[test]
fn actual_owned_fence_stage_recovers_to_a_startup_refusal_without_deleting_unknown_data() {
    let fixture = Fixture::new();
    drop(Spool::open(&fixture.configuration).unwrap());
    let operator = Spool::open_for_delivery(&fixture.configuration).unwrap();
    operator.fence(Uuid::new_v4()).unwrap();
    drop(operator);
    let root = &fixture.configuration.state_directory;
    let final_path = root.join("fence.json");
    let stage = root.join("fence.stage.json");
    let bytes = fs::read(&final_path).unwrap();
    fs::rename(&final_path, &stage).unwrap();
    assert!(matches!(
        Spool::open(&fixture.configuration),
        Err("GATEWAY_STATE_FENCED")
    ));
    assert_eq!(fs::read(&final_path).unwrap(), bytes);
    assert!(!stage.exists());
    fs::write(&stage, b"synthetic damaged fence stage").unwrap();
    fs::set_permissions(&stage, fs::Permissions::from_mode(0o600)).unwrap();
    assert!(Spool::open_for_delivery(&fixture.configuration).is_err());
    assert_eq!(fs::read(&stage).unwrap(), b"synthetic damaged fence stage");
    assert_eq!(fs::read(&final_path).unwrap(), bytes);
}

#[test]
fn actual_dangling_fence_symlink_is_preserved_and_cannot_bypass_startup_fencing() {
    let fixture = Fixture::new();
    drop(Spool::open(&fixture.configuration).unwrap());
    let path = fixture.configuration.state_directory.join("fence.json");
    std::os::unix::fs::symlink(fixture.root.join("missing-synthetic-target"), &path).unwrap();
    assert!(Spool::open(&fixture.configuration).is_err());
    assert!(fs::symlink_metadata(path).unwrap().file_type().is_symlink());
}

#[test]
fn actual_private_spool_reserves_capacity_locks_reopens_and_acknowledges_exact_metadata() {
    let fixture = Fixture::new();
    let spool = Spool::open(&fixture.configuration).unwrap();
    assert!(matches!(
        Spool::open(&fixture.configuration),
        Err("GATEWAY_STATE_LOCKED")
    ));
    let value = outcome();
    let first = spool.reserve(value.request_id).unwrap();
    let second = spool.reserve(Uuid::new_v4()).unwrap();
    assert!(!spool.ready());
    assert!(spool.reserve(Uuid::new_v4()).is_err());
    drop(second);
    assert!(spool.ready());
    first.complete(&value).unwrap();
    assert_eq!(
        spool.next_pending().unwrap().unwrap().digest().unwrap(),
        value.digest().unwrap()
    );
    drop(spool);
    let reopened = Spool::open(&fixture.configuration).unwrap();
    assert!(!reopened.ready());
    assert!(reopened.reserve(Uuid::new_v4()).is_err());
    assert_eq!(
        reopened.next_pending().unwrap().unwrap().request_id,
        value.request_id
    );
    let mut changed = value.clone();
    changed.output_started = true;
    assert!(reopened.acknowledge(&changed).is_err());
    reopened.acknowledge(&value).unwrap();
    reopened.acknowledge(&value).unwrap();
    assert!(reopened.next_pending().unwrap().is_none());
    assert!(reopened.ready());
}

#[test]
fn actual_failed_atomic_publication_preserves_unknown_file_then_recovers_owned_stage() {
    let fixture = Fixture::new();
    let spool = Spool::open(&fixture.configuration).unwrap();
    let value = outcome();
    let reservation = spool.reserve(value.request_id).unwrap();
    let final_path = fixture
        .configuration
        .state_directory
        .join("outcomes")
        .join(format!("{}.json", value.request_id));
    fs::write(&final_path, b"synthetic operator sentinel").unwrap();
    fs::set_permissions(&final_path, fs::Permissions::from_mode(0o600)).unwrap();
    assert!(reservation.complete(&value).is_err());
    assert!(!spool.ready());
    assert_eq!(
        fs::read(&final_path).unwrap(),
        b"synthetic operator sentinel"
    );
    drop(spool);
    assert!(Spool::open(&fixture.configuration).is_err());
    assert_eq!(
        fs::read(&final_path).unwrap(),
        b"synthetic operator sentinel"
    );
    // Remove only this explicitly created synthetic conflicting fixture, then exercise actual stage recovery.
    fs::remove_file(final_path).unwrap();
    let recovered = Spool::open(&fixture.configuration).unwrap();
    assert_eq!(
        recovered.next_pending().unwrap().unwrap().digest().unwrap(),
        value.digest().unwrap()
    );
    assert!(
        fs::read_dir(fixture.configuration.state_directory.join("outcomes"))
            .unwrap()
            .all(|entry| !entry
                .unwrap()
                .file_name()
                .to_str()
                .unwrap()
                .starts_with(".stage-"))
    );
}

#[test]
fn actual_spool_rejects_foreign_owner_corruption_and_nonempty_unowned_state_without_deleting() {
    let mut fixture = Fixture::new();
    let unknown = fixture.configuration.state_directory.join("keep");
    fs::write(&unknown, b"untouched").unwrap();
    assert!(matches!(
        Spool::open(&fixture.configuration),
        Err("GATEWAY_STATE_NOT_EMPTY")
    ));
    assert_eq!(fs::read(&unknown).unwrap(), b"untouched");
    fs::remove_file(unknown).unwrap();
    let spool = Spool::open(&fixture.configuration).unwrap();
    let value = outcome();
    spool
        .reserve(value.request_id)
        .unwrap()
        .complete(&value)
        .unwrap();
    drop(spool);
    let original = fixture.configuration.gateway_id;
    fixture.configuration.gateway_id = Uuid::new_v4();
    assert!(matches!(
        Spool::open(&fixture.configuration),
        Err("GATEWAY_STATE_OWNER_CONFLICT")
    ));
    fixture.configuration.gateway_id = original;
    let path = fixture
        .configuration
        .state_directory
        .join("outcomes")
        .join(format!("{}.json", value.request_id));
    fs::set_permissions(&path, fs::Permissions::from_mode(0o600)).unwrap();
    fs::write(&path, b"corrupt metadata preserved").unwrap();
    assert!(Spool::open(&fixture.configuration).is_err());
    assert_eq!(fs::read(path).unwrap(), b"corrupt metadata preserved");
}

#[test]
fn configured_free_space_gate_refuses_admission_without_filling_the_disk() {
    let mut fixture = Fixture::new();
    fixture.configuration.limits.minimum_free_disk_bytes = 9_007_199_254_740_991;
    assert!(
        crate::storage::filesystem_free_bytes(&fixture.root).unwrap()
            < fixture.configuration.limits.minimum_free_disk_bytes
    );
    let spool = Spool::open(&fixture.configuration).unwrap();
    assert!(!spool.ready());
    assert!(spool.reserve(Uuid::new_v4()).is_err());
}

#[test]
fn replaced_storage_directory_is_preserved_and_never_used_as_a_fallback() {
    let fixture = Fixture::new();
    let spool = Spool::open(&fixture.configuration).unwrap();
    let value = outcome();
    let reservation = spool.reserve(value.request_id).unwrap();
    let directory = fixture.configuration.state_directory.join("outcomes");
    let retained = fixture
        .configuration
        .state_directory
        .join("retained-outcomes");
    fs::rename(&directory, &retained).unwrap();
    fs::create_dir(&directory).unwrap();
    fs::set_permissions(&directory, fs::Permissions::from_mode(0o700)).unwrap();
    let sentinel = directory.join("unknown");
    fs::write(&sentinel, b"replacement preserved").unwrap();
    assert!(!spool.ready());
    assert!(matches!(
        reservation.complete(&value),
        Err("GATEWAY_SPOOL_LOCATION_CHANGED")
    ));
    assert_eq!(fs::read(&sentinel).unwrap(), b"replacement preserved");
    assert_eq!(fs::read_dir(&directory).unwrap().count(), 1);
    drop(spool);
    fs::remove_file(sentinel).unwrap();
    fs::remove_dir(directory.clone()).unwrap();
    fs::rename(retained, directory).unwrap();
    assert!(Spool::open(&fixture.configuration).unwrap().ready());
}
