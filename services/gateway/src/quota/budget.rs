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
use std::sync::{Arc, Mutex};
use time::OffsetDateTime;
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

struct State {
    healthy: bool,
    windows: [Ledger; 3],
}

pub(super) struct Budget {
    directory: PathBuf,
    snapshot: PathBuf,
    stage: PathBuf,
    state: Mutex<State>,
    clock: fn() -> i64,
    _lock: Arc<Flock<File>>,
}

impl Budget {
    pub(super) fn open(
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
            state: Mutex::new(State {
                healthy: true,
                windows,
            }),
        })
    }

    fn publish(&self, state: &mut State) -> ConfigResult<()> {
        let result = (|| {
            let bytes = serde_json::to_vec(&state.windows).map_err(|_| UNAVAILABLE)?;
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
        })();
        if result.is_err() {
            state.healthy = false;
        }
        result
    }

    pub(super) fn reserve(
        self: Arc<Self>,
        hierarchy: [Profile; 3],
        estimate: u64,
    ) -> Result<Hold, Failure> {
        let clock = self.clock;
        self.reserve_at(hierarchy, estimate, clock)
    }

    fn reserve_at(
        self: Arc<Self>,
        hierarchy: [Profile; 3],
        estimate: u64,
        clock: impl FnOnce() -> i64,
    ) -> Result<Hold, Failure> {
        let mut state = self.state.lock().map_err(|_| UNAVAILABLE)?;
        if !state.healthy {
            return Err(UNAVAILABLE.into());
        }
        let now = clock();
        let periods = periods(now)?;
        let mut starts = [None; 3];
        let mut denial = None;
        for (index, period) in periods.iter().enumerate() {
            let ledger = &state.windows[index];
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
        for (ledger, start) in state.windows.iter_mut().zip(starts) {
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
                    state.healthy = false;
                    return Err(UNAVAILABLE.into());
                };
                *charged = updated;
            }
        }
        self.publish(&mut state)?;
        drop(state);
        Ok(Hold {
            budget: self,
            ids,
            starts,
            estimate,
        })
    }
}

// Dropping a hold keeps the debit when upstream usage could not be verified.
pub(super) struct Hold {
    budget: Arc<Budget>,
    ids: [Uuid; 3],
    starts: [Option<i64>; 3],
    estimate: u64,
}

impl Hold {
    pub(super) fn settle(self, actual: u64) -> ConfigResult<()> {
        let refund = self.estimate.checked_sub(actual).ok_or(UNAVAILABLE)?;
        if refund == 0 {
            return Ok(());
        }
        let mut state = self.budget.state.lock().map_err(|_| UNAVAILABLE)?;
        if !state.healthy {
            return Err(UNAVAILABLE);
        }
        let mut changed = false;
        for (ledger, start) in state.windows.iter_mut().zip(self.starts) {
            if start != Some(ledger.start) {
                continue;
            }
            for id in self.ids {
                let charged = ledger.charged.get_mut(&id).ok_or(UNAVAILABLE)?;
                *charged = charged.checked_sub(refund).ok_or(UNAVAILABLE)?;
            }
            changed = true;
        }
        if changed {
            self.budget.publish(&mut state)?;
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests;
