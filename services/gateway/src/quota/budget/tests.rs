use super::*;
use crate::quota::{Ceiling, TokenCeiling};
use nix::fcntl::FlockArg;
use std::num::NonZeroU64;
use std::os::unix::fs::PermissionsExt;
use std::time::Instant;
use time::{Date, Month};

struct Directory(PathBuf, Arc<Flock<File>>);
impl Directory {
    fn new() -> Self {
        let root = std::env::temp_dir().join(format!("gateway-quota-{}", Uuid::new_v4()));
        fs::DirBuilder::new().mode(0o700).create(&root).unwrap();
        let root = root.canonicalize().unwrap();
        let lock = new_private_file(&root.join("test.lock")).unwrap();
        Self(
            root,
            Arc::new(Flock::lock(lock, FlockArg::LockExclusiveNonblock).unwrap()),
        )
    }

    fn open(&self, tenant: Uuid) -> Budget {
        Budget::open(
            &self.0,
            tenant,
            || OffsetDateTime::now_utc().unix_timestamp(),
            self.1.clone(),
        )
        .unwrap()
    }
}
impl Drop for Directory {
    fn drop(&mut self) {
        fs::remove_dir_all(&self.0).unwrap();
    }
}

fn at(year: i32, month: Month, day: u8, hour: u8, minute: u8, second: u8) -> i64 {
    Date::from_calendar_date(year, month, day)
        .unwrap()
        .with_hms(hour, minute, second)
        .unwrap()
        .assume_utc()
        .unix_timestamp()
}

fn hierarchy(tokens: TokenCeiling) -> [Profile; 3] {
    [
        Ceiling::default(),
        Ceiling {
            tokens: Some(tokens),
            ..Ceiling::default()
        },
        Ceiling::default(),
    ]
    .map(|limits| Profile {
        id: Uuid::new_v4(),
        slot: 0,
        limits,
    })
}

#[test]
fn each_budget_period_is_independent_and_reports_its_utc_reset() {
    let root = Directory::new();
    let now = at(2028, Month::February, 29, 22, 59, 55);
    for (index, wait) in [5, 3605, 3605].into_iter().enumerate() {
        let mut limits = [None; 3];
        limits[index] = NonZeroU64::new(30);
        let [hourly, daily, monthly] = limits;
        let path = hierarchy(TokenCeiling {
            hourly,
            daily,
            monthly,
        });
        let ledger = Arc::new(root.open(path[0].id));
        drop(ledger.clone().reserve_at(path, 24, || now).unwrap());
        let denied = ledger.clone().reserve_at(path, 7, || now).err().unwrap();
        assert_eq!(denied.code, CODES[index]);
        assert_eq!(denied.scope, Some("application"));
        assert_eq!(denied.retry_after, Some(wait));
        assert!(
            ledger
                .clone()
                .reserve_at(path, 7, || now + wait as i64 - 1)
                .is_err()
        );
        assert!(ledger.reserve_at(path, 7, || now + wait as i64).is_ok());
    }
    let december = at(2026, Month::December, 31, 23, 59, 59);
    assert_eq!(periods(december).unwrap()[2].end, december + 1);
}

#[test]
fn siblings_share_durable_debits_and_only_the_original_window_is_refunded() {
    let root = Directory::new();
    let now = at(2026, Month::November, 8, 4, 59, 59);
    let a = hierarchy(TokenCeiling {
        hourly: NonZeroU64::new(30),
        ..TokenCeiling::default()
    });
    let mut b = a;
    b[2].id = Uuid::new_v4();
    let tenant = a[0].id;
    let ledger = Arc::new(root.open(tenant));
    let first = ledger.clone().reserve_at(a, 20, || now).unwrap();
    assert!(ledger.clone().reserve_at(b, 11, || now).is_err());
    first.settle(8).unwrap();
    let old = ledger.clone().reserve_at(b, 20, || now).unwrap();
    drop(ledger.clone().reserve_at(a, 21, || now + 1).unwrap());
    old.settle(1).unwrap();
    drop(ledger);

    let ledger = Arc::new(root.open(tenant));
    let denied = ledger.clone().reserve_at(b, 10, || now + 1).err().unwrap();
    assert_eq!(denied.code, CODES[0]);
    assert!(ledger.clone().reserve_at(b, 9, || now + 1).is_ok());
    assert_eq!(
        ledger.reserve_at(a, 1, || now).err().unwrap().code,
        UNAVAILABLE
    );
}

#[test]
fn refunds_survive_restart_and_storage_failure_blocks_dispatch() {
    let root = Directory::new();
    let now = at(2026, Month::November, 8, 1, 0, 0);
    let path = hierarchy(TokenCeiling {
        daily: NonZeroU64::new(30),
        ..TokenCeiling::default()
    });
    let ledger = Arc::new(root.open(path[0].id));
    ledger
        .reserve_at(path, 20, || now)
        .unwrap()
        .settle(7)
        .unwrap();
    let ledger = Arc::new(root.open(path[0].id));
    assert_eq!(
        fs::metadata(&ledger.snapshot).unwrap().permissions().mode() & 0o777,
        0o600
    );
    assert!(ledger.clone().reserve_at(path, 23, || now).is_ok());

    // An unexpected stage blocks publication even though the new period has capacity.
    fs::create_dir(&ledger.stage).unwrap();
    assert_eq!(
        ledger
            .clone()
            .reserve_at(path, 1, || now + DAY)
            .err()
            .unwrap()
            .code,
        UNAVAILABLE
    );
    assert_eq!(
        ledger.reserve_at(path, 1, || now + DAY).err().unwrap().code,
        UNAVAILABLE
    );
}

#[test]
fn concurrent_reservations_cannot_exceed_the_shared_cap() {
    let root = Directory::new();
    let now = at(2026, Month::November, 8, 1, 0, 0);
    let path = hierarchy(TokenCeiling {
        monthly: NonZeroU64::new(50),
        ..TokenCeiling::default()
    });
    let ledger = Arc::new(root.open(path[0].id));
    let barrier = std::sync::Barrier::new(8);
    let admitted = std::thread::scope(|scope| {
        let workers: Vec<_> = (0..8)
            .map(|_| {
                let ledger = ledger.clone();
                let barrier = &barrier;
                scope.spawn(move || {
                    barrier.wait();
                    ledger.reserve_at(path, 10, || now).is_ok()
                })
            })
            .collect();
        workers
            .into_iter()
            .map(|thread| usize::from(thread.join().unwrap()))
            .sum::<usize>()
    });
    assert_eq!(admitted, 5);
}

#[test]
#[ignore = "run in release mode for latency measurements"]
fn durable_budget_benchmark() {
    let root = Directory::new();
    let path = hierarchy(TokenCeiling {
        monthly: NonZeroU64::new(100_000),
        ..TokenCeiling::default()
    });
    let ledger = Arc::new(root.open(path[0].id));
    let mut reserve = Vec::with_capacity(1000);
    let mut settle = Vec::with_capacity(1000);
    for _ in 0..1000 {
        let start = Instant::now();
        let hold = ledger.clone().reserve(path, 23).unwrap();
        reserve.push(start.elapsed());
        let start = Instant::now();
        hold.settle(9).unwrap();
        settle.push(start.elapsed());
    }
    for (label, mut samples) in [("reserve", reserve), ("settle", settle)] {
        samples.sort_unstable();
        println!(
            "{} durable {label}s; p50={} us; p95={} us; p99={} us",
            samples.len(),
            samples[500].as_micros(),
            samples[950].as_micros(),
            samples[990].as_micros()
        );
    }
}
