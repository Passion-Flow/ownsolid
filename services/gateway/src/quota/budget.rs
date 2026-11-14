use super::{Profile, Scope};
use crate::configuration::{Result as ConfigResult, file_bytes, private_directory};
use crate::errors::Failure;
use crate::storage::{new_private_file, sync_directory};
use nix::fcntl::Flock;
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use std::fs::{self, File};
use std::io::Write;
use std::os::unix::fs::DirBuilderExt;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use time::OffsetDateTime;
use tokio::sync::{mpsc, oneshot};
use uuid::Uuid;

const UNAVAILABLE: &str = "quota_state_unavailable";
const MAX_SNAPSHOT_BYTES: u64 = 8 * 1024 * 1024;
const HOUR: i64 = 3600;
const DAY: i64 = 24 * HOUR;
const CODES: [&str; 3] = [
    "hourly_token_budget_exceeded",
    "daily_token_budget_exceeded",
    "monthly_token_budget_exceeded",
];

struct Period {
    start: i64,
    end: i64,
}

fn periods(now: i64) -> ConfigResult<[Period; 3]> {
    let date = OffsetDateTime::from_unix_timestamp(now)
        .map_err(|_| UNAVAILABLE)?
        .date();
    let month = date.replace_day(1).map_err(|_| UNAVAILABLE)?;
    let next = month.month().next();
    let year = month.year() + i32::from(next == time::Month::January);
    let next_month = time::Date::from_calendar_date(year, next, 1).map_err(|_| UNAVAILABLE)?;
    Ok([
        Period {
            start: now.div_euclid(HOUR) * HOUR,
            end: (now.div_euclid(HOUR) + 1) * HOUR,
        },
        Period {
            start: now.div_euclid(DAY) * DAY,
            end: (now.div_euclid(DAY) + 1) * DAY,
        },
        Period {
            start: month.midnight().assume_utc().unix_timestamp(),
            end: next_month.midnight().assume_utc().unix_timestamp(),
        },
    ])
}

#[derive(Default, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Ledger {
    start: i64,
    charged: BTreeMap<Uuid, u64>,
}

#[derive(Clone)]
pub(super) struct Budget {
    changes: mpsc::Sender<Pending>,
}

struct Debit {
    ids: [Uuid; 3],
    starts: [Option<i64>; 3],
    estimate: u64,
}

enum Change {
    Reserve {
        hierarchy: [Profile; 3],
        estimate: u64,
    },
    Refund {
        debit: Debit,
        amount: u64,
    },
}

struct Pending {
    change: Change,
    reply: oneshot::Sender<Result<Option<Debit>, Failure>>,
}

impl Budget {
    pub(super) fn open(
        root: &Path,
        tenant: Uuid,
        clock: fn() -> i64,
        lock: Arc<Flock<File>>,
        capacity: usize,
    ) -> ConfigResult<Self> {
        let runtime = tokio::runtime::Handle::try_current().map_err(|_| UNAVAILABLE)?;
        let writer = Writer::open(root, tenant, clock, lock)?;
        let (changes, incoming) = mpsc::channel(capacity);
        runtime.spawn(writer.run(incoming, capacity));
        Ok(Self { changes })
    }

    async fn submit(&self, change: Change) -> Result<Option<Debit>, Failure> {
        let (reply, result) = oneshot::channel();
        self.changes
            .send(Pending { change, reply })
            .await
            .map_err(|_| UNAVAILABLE)?;
        result.await.map_err(|_| UNAVAILABLE)?
    }

    pub(super) async fn reserve(
        &self,
        hierarchy: [Profile; 3],
        estimate: u64,
    ) -> Result<Hold, Failure> {
        let debit = self
            .submit(Change::Reserve {
                hierarchy,
                estimate,
            })
            .await?
            .ok_or(UNAVAILABLE)?;
        Ok(Hold {
            budget: self.clone(),
            debit,
        })
    }
}

struct Writer {
    directory: PathBuf,
    snapshot: PathBuf,
    stage: PathBuf,
    windows: [Ledger; 3],
    healthy: bool,
    clock: fn() -> i64,
    _lock: Arc<Flock<File>>,
}

impl Writer {
    fn open(
        root: &Path,
        tenant: Uuid,
        clock: fn() -> i64,
        lock: Arc<Flock<File>>,
    ) -> ConfigResult<Self> {
        // Application holds the existing gateway state lock for the lifetime of this ledger.
        let directory = root.join("quotas");
        if !directory.try_exists().map_err(|_| UNAVAILABLE)? {
            fs::DirBuilder::new()
                .mode(0o700)
                .create(&directory)
                .map_err(|_| UNAVAILABLE)?;
            sync_directory(root).map_err(|_| UNAVAILABLE)?;
        }
        private_directory(&directory).map_err(|_| UNAVAILABLE)?;
        let snapshot = directory.join(format!("{tenant}.v1.json"));
        let stage = directory.join(format!("{tenant}.stage"));
        let windows = if snapshot.try_exists().map_err(|_| UNAVAILABLE)? {
            let bytes = file_bytes(&snapshot, MAX_SNAPSHOT_BYTES, true).map_err(|_| UNAVAILABLE)?;
            serde_json::from_slice(&bytes).map_err(|_| UNAVAILABLE)?
        } else {
            Default::default()
        };
        // Dispatch only follows publication and directory sync, never a staged write.
        match fs::remove_file(&stage) {
            Ok(()) => sync_directory(&directory).map_err(|_| UNAVAILABLE)?,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
            Err(_) => return Err(UNAVAILABLE),
        }
        Ok(Self {
            directory,
            snapshot,
            stage,
            clock,
            _lock: lock,
            healthy: true,
            windows,
        })
    }

    async fn run(mut self, mut incoming: mpsc::Receiver<Pending>, capacity: usize) {
        let mut batch = Vec::with_capacity(capacity);
        while incoming.recv_many(&mut batch, capacity).await != 0 {
            // One blocking job per ready batch lets other tenants use the pool
            // between writes. The writer retains the state lock during I/O.
            let Ok((writer, reusable)) = tokio::task::spawn_blocking(move || {
                self.commit(batch.drain(..));
                (self, batch)
            })
            .await
            else {
                return;
            };
            if !writer.healthy {
                return;
            }
            self = writer;
            batch = reusable;
        }
    }

    fn commit(&mut self, batch: impl Iterator<Item = Pending>) {
        let mut changed = false;
        let mut replies = Vec::with_capacity(batch.size_hint().0);
        for Pending { change, reply } in batch {
            if matches!(&change, Change::Reserve { .. }) && reply.is_closed() {
                continue;
            }
            let result = if !self.healthy {
                Err(UNAVAILABLE.into())
            } else {
                match change {
                    Change::Reserve {
                        hierarchy,
                        estimate,
                    } => self
                        .reserve(hierarchy, estimate, (self.clock)())
                        .map(|debit| {
                            changed = true;
                            Some(debit)
                        }),
                    Change::Refund { debit, amount } => self
                        .refund(debit, amount)
                        .map(|refunded| {
                            changed |= refunded;
                            None
                        })
                        .inspect_err(|_| self.healthy = false),
                }
            };
            replies.push((reply, result));
        }
        if self.healthy && changed {
            self.healthy = self.publish().is_ok();
        }
        for (reply, result) in replies {
            let result = if self.healthy {
                result
            } else {
                Err(UNAVAILABLE.into())
            };
            let _ = reply.send(result);
        }
    }

    fn publish(&self) -> ConfigResult<()> {
        let bytes = serde_json::to_vec(&self.windows).map_err(|_| UNAVAILABLE)?;
        if bytes.len() as u64 > MAX_SNAPSHOT_BYTES {
            return Err(UNAVAILABLE);
        }
        private_directory(&self.directory).map_err(|_| UNAVAILABLE)?;
        let mut staged = new_private_file(&self.stage).map_err(|_| UNAVAILABLE)?;
        staged
            .write_all(&bytes)
            .and_then(|_| staged.sync_all())
            .map_err(|_| UNAVAILABLE)?;
        fs::rename(&self.stage, &self.snapshot).map_err(|_| UNAVAILABLE)?;
        sync_directory(&self.directory).map_err(|_| UNAVAILABLE)
    }

    fn reserve(
        &mut self,
        hierarchy: [Profile; 3],
        estimate: u64,
        now: i64,
    ) -> Result<Debit, Failure> {
        let periods = periods(now)?;
        let mut starts = [None; 3];
        let mut denial = None;
        for (index, period) in periods.iter().enumerate() {
            let ledger = &self.windows[index];
            if ledger.start > period.start {
                return Err(UNAVAILABLE.into());
            }
            for (scope, profile) in Scope::ALL.into_iter().zip(hierarchy) {
                let Some(limit) = profile.limits.tokens()[index] else {
                    continue;
                };
                starts[index] = Some(period.start);
                let charged = if ledger.start == period.start {
                    ledger.charged.get(&profile.id).copied().unwrap_or(0)
                } else {
                    0
                };
                if estimate > limit.get().saturating_sub(charged) {
                    let retry = (period.end - now) as u64;
                    if denial
                        .as_ref()
                        .is_none_or(|prior: &Failure| prior.retry_after.unwrap_or(0) < retry)
                    {
                        denial = Some(Failure::throttled(CODES[index], scope.name(), retry));
                    }
                }
            }
        }
        if let Some(denial) = denial {
            return Err(denial);
        }
        let ids = hierarchy.map(|profile| profile.id);
        for (ledger, start) in self.windows.iter_mut().zip(starts) {
            let Some(start) = start else { continue };
            if ledger.start != start {
                *ledger = Ledger {
                    start,
                    charged: BTreeMap::new(),
                };
            }
            for id in ids {
                let charged = ledger.charged.entry(id).or_default();
                let Some(updated) = charged.checked_add(estimate) else {
                    self.healthy = false;
                    return Err(UNAVAILABLE.into());
                };
                *charged = updated;
            }
        }
        Ok(Debit {
            ids,
            starts,
            estimate,
        })
    }

    fn refund(&mut self, debit: Debit, amount: u64) -> Result<bool, Failure> {
        let mut changed = false;
        for (ledger, start) in self.windows.iter_mut().zip(debit.starts) {
            if start != Some(ledger.start) {
                continue;
            }
            for id in debit.ids {
                let charged = ledger.charged.get_mut(&id).ok_or(UNAVAILABLE)?;
                *charged = charged.checked_sub(amount).ok_or(UNAVAILABLE)?;
            }
            changed = true;
        }
        Ok(changed)
    }
}

// Dropping a hold keeps the debit when upstream usage could not be verified.
pub(super) struct Hold {
    budget: Budget,
    debit: Debit,
}

impl Hold {
    pub(super) async fn settle(self, actual: u64) -> ConfigResult<()> {
        let amount = self.debit.estimate.checked_sub(actual).ok_or(UNAVAILABLE)?;
        if amount == 0 {
            return Ok(());
        }
        self.budget
            .submit(Change::Refund {
                debit: self.debit,
                amount,
            })
            .await
            .map(|_| ())
            .map_err(|error| error.code)
    }
}

#[cfg(test)]
mod tests;
