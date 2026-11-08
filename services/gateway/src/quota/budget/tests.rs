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
        Budget::open(&self.0, tenant, now, self.1.clone(), 8).unwrap()
    }

    fn writer(&self, tenant: Uuid) -> Writer {
        Writer::open(&self.0, tenant, now, self.1.clone()).unwrap()
    }
}
impl Drop for Directory {
    fn drop(&mut self) {
        fs::remove_dir_all(&self.0).unwrap();
    }
}

fn now() -> i64 {
    at(2026, Month::November, 8, 1, 0, 0)
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
        let mut ledger = root.writer(path[0].id);
        assert!(ledger.reserve(path, 24, now).is_ok());
        let denied = ledger.reserve(path, 7, now).err().unwrap();
        assert_eq!(denied.code, CODES[index]);
        assert_eq!(denied.scope, Some("application"));
        assert_eq!(denied.retry_after, Some(wait));
        assert!(ledger.reserve(path, 7, now + wait as i64 - 1).is_err());
        assert!(ledger.reserve(path, 7, now + wait as i64).is_ok());
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
    let mut ledger = root.writer(tenant);
    let first = ledger.reserve(a, 20, now).unwrap();
    assert!(ledger.reserve(b, 11, now).is_err());
    assert!(ledger.refund(first, 12).unwrap());
    let old = ledger.reserve(b, 20, now).unwrap();
    assert!(ledger.reserve(a, 21, now + 1).is_ok());
    assert!(!ledger.refund(old, 19).unwrap());
    ledger.publish().unwrap();

    let mut ledger = root.writer(tenant);
    let denied = ledger.reserve(b, 10, now + 1).err().unwrap();
    assert_eq!(denied.code, CODES[0]);
    assert!(ledger.reserve(b, 9, now + 1).is_ok());
    assert_eq!(ledger.reserve(a, 1, now).err().unwrap().code, UNAVAILABLE);
}

#[tokio::test]
async fn refunds_survive_reopening_and_storage_failure_blocks_dispatch() {
    let root = Directory::new();
    let path = hierarchy(TokenCeiling {
        daily: NonZeroU64::new(30),
        ..TokenCeiling::default()
    });
    let ledger = root.open(path[0].id);
    ledger
        .reserve(path, 20)
        .await
        .unwrap()
        .settle(7)
        .await
        .unwrap();
    drop(ledger);

    let disk = root.writer(path[0].id);
    assert_eq!(
        fs::metadata(&disk.snapshot).unwrap().permissions().mode() & 0o777,
        0o600
    );
    let ledger = root.open(path[0].id);
    let remaining = ledger.reserve(path, 23).await.unwrap();
    fs::create_dir(&disk.stage).unwrap();
    assert_eq!(remaining.settle(22).await, Err(UNAVAILABLE));
    assert_eq!(
        ledger.reserve(path, 1).await.err().unwrap().code,
        UNAVAILABLE
    );
}

#[tokio::test]
async fn concurrent_reservations_cannot_exceed_the_shared_cap() {
    let root = Directory::new();
    let path = hierarchy(TokenCeiling {
        monthly: NonZeroU64::new(50),
        ..TokenCeiling::default()
    });
    let ledger = root.open(path[0].id);
    let barrier = Arc::new(tokio::sync::Barrier::new(8));
    let workers: Vec<_> = (0..8)
        .map(|_| {
            let ledger = ledger.clone();
            let barrier = barrier.clone();
            tokio::spawn(async move {
                barrier.wait().await;
                ledger.reserve(path, 10).await.is_ok()
            })
        })
        .collect();
    let mut admitted = 0;
    for worker in workers {
        admitted += usize::from(worker.await.unwrap());
    }
    assert_eq!(admitted, 5);
}

#[tokio::test]
async fn a_batch_keeps_fifo_capacity_and_commits_verified_refunds_after_cancellation() {
    let root = Directory::new();
    let path = hierarchy(TokenCeiling {
        monthly: NonZeroU64::new(30),
        ..TokenCeiling::default()
    });
    let mut writer = root.writer(path[0].id);
    let debit = writer.reserve(path, 24, now()).unwrap();
    writer.publish().unwrap();

    let (denied, denied_result) = oneshot::channel();
    let (refund, canceled_refund) = oneshot::channel();
    let (canceled, canceled_result) = oneshot::channel();
    let (accepted, accepted_result) = oneshot::channel();
    drop(canceled_refund);
    drop(canceled_result);
    let reserve = || Change::Reserve {
        hierarchy: path,
        estimate: 10,
    };
    writer.commit(
        [
            Pending {
                change: reserve(),
                reply: denied,
            },
            Pending {
                change: Change::Refund { debit, amount: 17 },
                reply: refund,
            },
            Pending {
                change: reserve(),
                reply: canceled,
            },
            Pending {
                change: reserve(),
                reply: accepted,
            },
        ]
        .into_iter(),
    );
    assert_eq!(denied_result.await.unwrap().err().unwrap().code, CODES[2]);
    assert!(accepted_result.await.unwrap().unwrap().is_some());

    let mut reopened = root.writer(path[0].id);
    assert!(reopened.reserve(path, 13, now()).is_ok());
    assert!(reopened.reserve(path, 1, now()).is_err());
}

#[tokio::test]
async fn failed_batch_publication_rejects_every_tentative_reservation() {
    let root = Directory::new();
    let path = hierarchy(TokenCeiling {
        monthly: NonZeroU64::new(30),
        ..TokenCeiling::default()
    });
    let mut writer = root.writer(path[0].id);
    let debit = writer.reserve(path, 24, now()).unwrap();
    writer.publish().unwrap();
    fs::create_dir(&writer.stage).unwrap();
    let (refund, refund_result) = oneshot::channel();
    let (reserve, reserve_result) = oneshot::channel();
    writer.commit(
        [
            Pending {
                change: Change::Refund { debit, amount: 17 },
                reply: refund,
            },
            Pending {
                change: Change::Reserve {
                    hierarchy: path,
                    estimate: 10,
                },
                reply: reserve,
            },
        ]
        .into_iter(),
    );
    assert_eq!(
        refund_result.await.unwrap().err().unwrap().code,
        UNAVAILABLE
    );
    assert_eq!(
        reserve_result.await.unwrap().err().unwrap().code,
        UNAVAILABLE
    );
    fs::remove_dir(&writer.stage).unwrap();

    let mut reopened = root.writer(path[0].id);
    assert!(reopened.reserve(path, 7, now()).is_err());
    assert!(reopened.reserve(path, 6, now()).is_ok());
}

#[tokio::test]
#[ignore = "run in release mode for latency measurements"]
async fn durable_budget_benchmark() {
    let root = Directory::new();
    let path = hierarchy(TokenCeiling {
        monthly: NonZeroU64::new(100_000),
        ..TokenCeiling::default()
    });
    let ledger = root.open(path[0].id);
    let mut reserve = Vec::with_capacity(1000);
    let mut settle = Vec::with_capacity(1000);
    for _ in 0..1000 {
        let start = Instant::now();
        let hold = ledger.reserve(path, 23).await.unwrap();
        reserve.push(start.elapsed());
        let start = Instant::now();
        hold.settle(9).await.unwrap();
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

#[tokio::test(flavor = "multi_thread", worker_threads = 8)]
#[ignore = "run in release mode for latency measurements"]
async fn contended_budget_benchmark() {
    const WORKERS: usize = 8;
    const PAIRS: usize = 25;
    let root = Directory::new();
    let path = hierarchy(TokenCeiling {
        monthly: NonZeroU64::new(100_000),
        ..TokenCeiling::default()
    });
    let ledger = root.open(path[0].id);
    let barrier = Arc::new(tokio::sync::Barrier::new(WORKERS));
    let started = Instant::now();
    let workers: Vec<_> = (0..WORKERS)
        .map(|_| {
            let ledger = ledger.clone();
            let barrier = barrier.clone();
            tokio::spawn(async move {
                barrier.wait().await;
                let mut samples = Vec::with_capacity(PAIRS);
                for _ in 0..PAIRS {
                    let start = Instant::now();
                    ledger
                        .reserve(path, 23)
                        .await
                        .unwrap()
                        .settle(9)
                        .await
                        .unwrap();
                    samples.push(start.elapsed());
                }
                samples
            })
        })
        .collect();
    let mut samples = Vec::with_capacity(WORKERS * PAIRS);
    for worker in workers {
        samples.extend(worker.await.unwrap());
    }
    let elapsed = started.elapsed();
    samples.sort_unstable();
    println!(
        "{} pairs; {} workers; {:.1} pairs/s; p50={} us; p95={} us; p99={} us",
        samples.len(),
        WORKERS,
        samples.len() as f64 / elapsed.as_secs_f64(),
        samples[samples.len() / 2].as_micros(),
        samples[samples.len() * 95 / 100].as_micros(),
        samples[samples.len() * 99 / 100].as_micros()
    );
}
