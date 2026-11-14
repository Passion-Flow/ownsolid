use super::*;
use crate::quota::{Account, Ceiling, Profile, Tenant, Traffic};
use std::num::NonZeroU32;

fn authority(rate: Option<NonZeroU32>, concurrent: Option<NonZeroU32>) -> (Authority, Uuid, Uuid) {
    let workspace = Uuid::new_v4();
    let key = Uuid::new_v4();
    let hierarchy = std::array::from_fn(|slot| Profile {
        id: if slot == 2 { key } else { Uuid::new_v4() },
        slot,
        limits: if slot == 1 {
            Ceiling {
                requests_per_second: rate,
                concurrent_requests: concurrent,
                ..Ceiling::default()
            }
        } else {
            Ceiling::default()
        },
    });
    let tenant = Arc::new(Tenant {
        traffic: Mutex::new((0..3).map(|_| Traffic::default()).collect()),
        budget: None,
    });
    let quotas = Arc::new(Quotas {
        keys: HashMap::from([(
            key,
            Arc::new(Account {
                workspace,
                tenant,
                hierarchy,
            }),
        )]),
    });
    (
        Authority {
            quotas,
            peers: HashMap::new(),
            leases: Mutex::new(Leases::default()),
            stopped: AtomicBool::new(false),
            lease_seconds: 20,
            maximum_leases: 8,
            ready_at: Instant::now(),
            body_timeout: Duration::from_secs(1),
        },
        workspace,
        key,
    )
}

#[tokio::test]
async fn peers_share_concurrency_cannot_cross_workspaces_or_touch_another_lease() {
    let (authority, workspace_id, key_id) = authority(None, NonZeroU32::new(1));
    let left = Uuid::new_v4();
    let right = Uuid::new_v4();
    let acquire = || Command::Acquire {
        workspace_id,
        key_id,
    };
    let grant = authority
        .execute(left, workspace_id, acquire())
        .await
        .unwrap()
        .unwrap();
    let error = authority
        .execute(right, Uuid::new_v4(), acquire())
        .await
        .err()
        .unwrap();
    assert_eq!(error.code, "quota_scope_unconfigured");
    let reserve = || Command::Reserve {
        lease_id: grant.lease_id,
        estimate: 23,
    };
    assert_eq!(
        authority
            .execute(right, workspace_id, reserve())
            .await
            .err()
            .unwrap()
            .code,
        "quota_lease_unavailable"
    );
    assert!(
        authority
            .execute(left, workspace_id, reserve())
            .await
            .is_ok()
    );
    assert!(
        authority
            .execute(left, workspace_id, reserve())
            .await
            .is_err()
    );
    let finish = || Command::Finish {
        lease_id: grant.lease_id,
        actual: Some(9),
    };
    assert!(
        authority
            .execute(right, workspace_id, finish())
            .await
            .is_err()
    );
    let denied = authority
        .execute(right, workspace_id, acquire())
        .await
        .err()
        .unwrap();
    assert_eq!(denied.code, "concurrency_limit_exceeded");
    assert_eq!(denied.scope, Some("application"));
    assert!(
        authority
            .execute(left, workspace_id, finish())
            .await
            .is_ok()
    );
    assert!(
        authority
            .execute(left, workspace_id, finish())
            .await
            .is_err()
    );
    assert!(
        authority
            .execute(right, workspace_id, acquire())
            .await
            .is_ok()
    );
}

#[tokio::test]
async fn peers_share_a_rolling_window_even_after_finish() {
    let (authority, workspace_id, key_id) = authority(NonZeroU32::new(1), None);
    let left = Uuid::new_v4();
    let right = Uuid::new_v4();
    let acquire = || Command::Acquire {
        workspace_id,
        key_id,
    };
    let grant = authority
        .execute(left, workspace_id, acquire())
        .await
        .unwrap()
        .unwrap();
    assert!(
        authority
            .execute(
                left,
                workspace_id,
                Command::Finish {
                    lease_id: grant.lease_id,
                    actual: None,
                }
            )
            .await
            .is_ok()
    );
    let denied = authority
        .execute(right, workspace_id, acquire())
        .await
        .err()
        .unwrap();
    assert_eq!(denied.code, "request_rate_exceeded");
    assert_eq!(denied.retry_after, Some(1));
}

#[tokio::test]
async fn expired_leases_release_capacity_and_restart_rejects_new_admission() {
    let (mut authority, workspace_id, key_id) = authority(None, NonZeroU32::new(1));
    let peer = Uuid::new_v4();
    let acquire = || Command::Acquire {
        workspace_id,
        key_id,
    };
    let grant = authority
        .execute(peer, workspace_id, acquire())
        .await
        .unwrap()
        .unwrap();
    authority
        .leases
        .lock()
        .unwrap()
        .prune(Instant::now() + Duration::from_secs(21));
    assert!(
        authority
            .execute(
                peer,
                workspace_id,
                Command::Reserve {
                    lease_id: grant.lease_id,
                    estimate: 1
                }
            )
            .await
            .is_err()
    );
    assert!(
        authority
            .execute(peer, workspace_id, acquire())
            .await
            .is_ok()
    );
    authority.ready_at = Instant::now() + Duration::from_secs(20);
    assert!(!authority.ready());
    assert!(
        authority
            .execute(peer, workspace_id, acquire())
            .await
            .err()
            .unwrap()
            .retry_after
            .is_some()
    );
}

#[test]
fn durable_marker_fences_authority_restart() {
    let directory = std::env::temp_dir().join(format!("quota-authority-{}", Uuid::new_v4()));
    fs::DirBuilder::new()
        .mode(0o700)
        .create(&directory)
        .unwrap();
    let root = directory.canonicalize().unwrap();
    assert_eq!(restart_fence(&root).unwrap(), Duration::ZERO);
    assert_eq!(
        restart_fence(&root).unwrap(),
        Duration::from_secs(MAX_LEASE_SECONDS)
    );
    fs::remove_dir_all(directory).unwrap();
}
