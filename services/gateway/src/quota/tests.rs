use super::*;
use serde_json::json;
use std::hint::black_box;

fn accounts(limits: [Ceiling; 3]) -> [Arc<Account>; 2] {
    let tenant = Arc::new(Tenant {
        traffic: Mutex::new((0..4).map(|_| Traffic::default()).collect()),
        budget: None,
    });
    let workspace = Uuid::new_v4();
    let profile = |slot: usize, index: usize| Profile {
        id: Uuid::new_v4(),
        slot,
        limits: limits[index],
    };
    let root = profile(0, 0);
    let app = profile(1, 1);
    [2, 3].map(|slot| {
        Arc::new(Account {
            workspace,
            tenant: tenant.clone(),
            hierarchy: [root, app, profile(slot, 2)],
        })
    })
}

#[test]
fn requests_use_a_rolling_second_shared_by_sibling_keys() {
    let [a, b] = accounts([
        Ceiling::default(),
        Ceiling {
            requests_per_second: NonZeroU32::new(2),
            ..Ceiling::default()
        },
        Ceiling::default(),
    ]);
    let first = Instant::now();
    let start = first + Duration::from_millis(990);
    assert!(a.acquire(|| start).is_ok());
    assert!(b.acquire(|| start).is_ok());
    let error = b.acquire(|| first + Duration::from_secs(1)).err().unwrap();
    assert_eq!(error.code, "request_rate_exceeded");
    assert_eq!(error.scope, Some("application"));
    assert_eq!(error.retry_after, Some(1));
    assert!(
        a.acquire(|| start + RATE_WINDOW - Duration::from_nanos(1))
            .is_err()
    );
    assert!(a.acquire(|| start + RATE_WINDOW).is_ok());
}

#[test]
fn concurrency_is_shared_by_siblings_but_isolated_between_tenants() {
    let limits = [
        Ceiling::default(),
        Ceiling {
            concurrent_requests: NonZeroU32::new(1),
            ..Ceiling::default()
        },
        Ceiling::default(),
    ];
    let [a, b] = accounts(limits);
    let [other, _] = accounts(limits);
    let held = a.acquire(Instant::now).unwrap();
    let error = b.acquire(Instant::now).err().unwrap();
    assert_eq!(error.code, "concurrency_limit_exceeded");
    assert_eq!(error.scope, Some("application"));
    assert_eq!(error.retry_after, Some(1));
    assert!(other.acquire(Instant::now).is_ok());
    drop(held);
    assert!(b.acquire(Instant::now).is_ok());
}

#[test]
fn authenticated_scope_cannot_cross_workspaces_or_use_an_unmapped_key() {
    let [a, _] = accounts([Ceiling::default(); 3]);
    let key = a.hierarchy[2].id;
    let workspace = a.workspace;
    let quotas = Quotas {
        keys: HashMap::from([(key, a)]),
    };
    let credential = format!("pk1.{key}.{}", Uuid::new_v4());
    assert!(quotas.acquire(workspace, &credential).is_ok());
    for (workspace, credential) in [
        (Uuid::new_v4(), credential),
        (
            workspace,
            format!("pk1.{}.{}", Uuid::new_v4(), Uuid::new_v4()),
        ),
    ] {
        assert_eq!(
            quotas.acquire(workspace, &credential).err().unwrap().code,
            "quota_scope_unconfigured"
        );
    }
}

#[test]
fn configuration_rejects_limits_above_any_ancestor_and_invalid_scope_ids() {
    let workspace = Uuid::new_v4();
    let mut config = json!({
        "tenants": [{
            "id": Uuid::new_v4(),
            "workspaces": [workspace],
            "limits": {"requests_per_second": 5, "concurrent_requests": 2, "tokens": {"hourly": 100}},
            "applications": [{
                "id": Uuid::new_v4(),
                "keys": [{
                    "id": Uuid::new_v4(), "workspace_id": workspace,
                    "limits": {"requests_per_second": 5, "concurrent_requests": 1, "tokens": {"hourly": 100}}
                }]
            }]
        }]
    });
    let validate = |value| serde_json::from_value::<Policy>(value).unwrap().validate();
    assert_eq!(validate(config.clone()), Ok(()));
    let key = "/tenants/0/applications/0/keys/0";
    for (field, high) in [
        ("requests_per_second", 6),
        ("concurrent_requests", 3),
        ("tokens/hourly", 101),
    ] {
        let mut invalid = config.clone();
        *invalid
            .pointer_mut(&format!("{key}/limits/{field}"))
            .unwrap() = json!(high);
        assert_eq!(validate(invalid), Err("GATEWAY_QUOTA_EXCEEDS_ANCESTOR"));
    }
    let mut invalid = config.clone();
    *invalid.pointer_mut(&format!("{key}/workspace_id")).unwrap() = json!(Uuid::new_v4());
    assert_eq!(validate(invalid), Err("GATEWAY_QUOTA_WORKSPACE_MISMATCH"));
    let keys = config
        .pointer_mut("/tenants/0/applications/0/keys")
        .unwrap()
        .as_array_mut()
        .unwrap();
    keys.push(keys[0].clone());
    assert_eq!(validate(config), Err("GATEWAY_QUOTA_SCOPE_INVALID"));
}

#[test]
#[ignore = "run in release mode for latency measurements"]
fn quota_check_benchmark() {
    const THREADS: usize = 8;
    const TENANTS: usize = 128;
    const ITERATIONS: usize = 100_000;
    let mut keys = HashMap::new();
    let fixtures: Vec<_> = (0..TENANTS)
        .map(|_| {
            let limits = Ceiling {
                requests_per_second: NonZeroU32::new(MAX_REQUESTS_PER_SECOND),
                concurrent_requests: NonZeroU32::new(THREADS as u32),
                ..Ceiling::default()
            };
            let [account, _] = accounts([limits; 3]);
            let key = account.hierarchy[2].id;
            let workspace = account.workspace;
            keys.insert(key, account);
            (workspace, format!("pk1.{key}.{}", Uuid::new_v4()))
        })
        .collect();
    let quotas = Quotas { keys };
    let barrier = std::sync::Barrier::new(THREADS);
    let start = Instant::now();
    let mut samples = std::thread::scope(|scope| {
        let workers: Vec<_> = (0..THREADS)
            .map(|worker| {
                let fixtures = &fixtures;
                let quotas = &quotas;
                let barrier = &barrier;
                scope.spawn(move || {
                    let mut samples = Vec::with_capacity(ITERATIONS);
                    barrier.wait();
                    for index in 0..ITERATIONS {
                        let (workspace, key) = &fixtures[(index + worker) % TENANTS];
                        let start = Instant::now();
                        let permit = black_box(quotas.acquire(*workspace, key).unwrap());
                        samples.push(start.elapsed());
                        drop(permit);
                    }
                    samples
                })
            })
            .collect();
        workers
            .into_iter()
            .flat_map(|worker| worker.join().unwrap())
            .collect::<Vec<_>>()
    });
    let elapsed = start.elapsed();
    samples.sort_unstable();
    println!(
        "{} checks; {} workers; {} tenants; {:.0} checks/s; p50={} ns; p95={} ns; p99={} ns",
        samples.len(),
        THREADS,
        TENANTS,
        samples.len() as f64 / elapsed.as_secs_f64(),
        samples[samples.len() / 2].as_nanos(),
        samples[samples.len() * 95 / 100].as_nanos(),
        samples[samples.len() * 99 / 100].as_nanos(),
    );
}
