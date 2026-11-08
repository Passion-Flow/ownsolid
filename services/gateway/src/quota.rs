//! Local quotas for keys whose authority was verified by control.
mod budget;

use crate::configuration::{Configuration, Result as ConfigResult};
use crate::errors::Failure;
use crate::storage::Spool;
use budget::{Budget, Hold};
use serde::{Deserialize, Serialize};
use std::collections::{HashMap, HashSet, VecDeque};
use std::num::{NonZeroU32, NonZeroU64};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};
use uuid::Uuid;

const RATE_WINDOW: Duration = Duration::from_secs(1);
const MAX_REQUESTS_PER_SECOND: u32 = 10_000;

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct Policy {
    tenants: Vec<TenantPolicy>,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct TenantPolicy {
    id: Uuid,
    workspaces: Vec<Uuid>,
    #[serde(default)]
    limits: Ceiling,
    applications: Vec<ApplicationPolicy>,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct ApplicationPolicy {
    id: Uuid,
    #[serde(default)]
    limits: Ceiling,
    keys: Vec<KeyPolicy>,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct KeyPolicy {
    id: Uuid,
    workspace_id: Uuid,
    #[serde(default)]
    limits: Ceiling,
}

#[derive(Clone, Copy, Default, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Ceiling {
    requests_per_second: Option<NonZeroU32>,
    concurrent_requests: Option<NonZeroU32>,
    tokens: Option<TokenCeiling>,
}

#[derive(Clone, Copy, Default, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct TokenCeiling {
    hourly: Option<NonZeroU64>,
    daily: Option<NonZeroU64>,
    monthly: Option<NonZeroU64>,
}

impl Ceiling {
    fn tokens(self) -> [Option<NonZeroU64>; 3] {
        let value = self.tokens.unwrap_or_default();
        [value.hourly, value.daily, value.monthly]
    }

    fn values(self) -> [Option<u64>; 5] {
        let [hourly, daily, monthly] = self.tokens().map(|limit| limit.map(NonZeroU64::get));
        [
            self.requests_per_second.map(|limit| u64::from(limit.get())),
            self.concurrent_requests.map(|limit| u64::from(limit.get())),
            hourly,
            daily,
            monthly,
        ]
    }

    fn validate(self, ancestors: &[Ceiling]) -> ConfigResult<()> {
        if self
            .requests_per_second
            .is_some_and(|limit| limit.get() > MAX_REQUESTS_PER_SECOND)
        {
            return Err("GATEWAY_QUOTA_RATE_INVALID");
        }
        if ancestors.iter().any(|parent| {
            self.values().into_iter().zip(parent.values()).any(|(child, parent)| {
                matches!((child, parent), (Some(child), Some(parent)) if child > parent)
            })
        }) {
            return Err("GATEWAY_QUOTA_EXCEEDS_ANCESTOR");
        }
        Ok(())
    }
}

impl Policy {
    pub(crate) fn validate(&self) -> ConfigResult<()> {
        fn unique(ids: &mut HashSet<Uuid>, id: Uuid) -> ConfigResult<()> {
            if id.is_nil() || !ids.insert(id) {
                return Err("GATEWAY_QUOTA_SCOPE_INVALID");
            }
            Ok(())
        }

        if self.tenants.is_empty() {
            return Err("GATEWAY_QUOTA_SCOPE_INVALID");
        }
        let mut scopes = HashSet::new();
        let mut workspaces = HashSet::new();
        for tenant in &self.tenants {
            unique(&mut scopes, tenant.id)?;
            tenant.limits.validate(&[])?;
            if tenant.workspaces.is_empty() {
                return Err("GATEWAY_QUOTA_SCOPE_INVALID");
            }
            for &workspace in &tenant.workspaces {
                unique(&mut workspaces, workspace)?;
            }
            for app in &tenant.applications {
                unique(&mut scopes, app.id)?;
                app.limits.validate(&[tenant.limits])?;
                for key in &app.keys {
                    unique(&mut scopes, key.id)?;
                    key.limits.validate(&[tenant.limits, app.limits])?;
                    if !tenant.workspaces.contains(&key.workspace_id) {
                        return Err("GATEWAY_QUOTA_WORKSPACE_MISMATCH");
                    }
                }
            }
        }
        Ok(())
    }
}

#[derive(Clone, Copy)]
enum Scope {
    Tenant,
    Application,
    Key,
}

impl Scope {
    const ALL: [Self; 3] = [Self::Tenant, Self::Application, Self::Key];

    const fn name(self) -> &'static str {
        match self {
            Self::Tenant => "tenant",
            Self::Application => "application",
            Self::Key => "key",
        }
    }
}

#[derive(Clone, Copy)]
struct Profile {
    id: Uuid,
    slot: usize,
    limits: Ceiling,
}

#[derive(Default)]
struct Traffic {
    starts: VecDeque<Instant>,
    active: u32,
}

struct Tenant {
    traffic: Mutex<Vec<Traffic>>,
    budget: Option<Budget>,
}

#[derive(Clone)]
struct Account {
    workspace: Uuid,
    tenant: Arc<Tenant>,
    hierarchy: [Profile; 3],
}

pub(crate) struct Quotas {
    keys: HashMap<Uuid, Account>,
}

impl Quotas {
    pub(crate) fn open(
        configuration: &Configuration,
        spool: &Spool,
        clock: fn() -> i64,
    ) -> ConfigResult<Option<Self>> {
        let Some(policy) = &configuration.quota_policy else {
            return Ok(None);
        };
        let mut keys = HashMap::new();
        for tenant in &policy.tenants {
            let mut slots = 1;
            let root = Profile {
                id: tenant.id,
                slot: 0,
                limits: tenant.limits,
            };
            let mut budget_needed = root.limits.tokens().iter().any(Option::is_some);
            let mut accounts = Vec::new();
            for app in &tenant.applications {
                let parent = Profile {
                    id: app.id,
                    slot: slots,
                    limits: app.limits,
                };
                slots += 1;
                for key in &app.keys {
                    let child = Profile {
                        id: key.id,
                        slot: slots,
                        limits: key.limits,
                    };
                    slots += 1;
                    let hierarchy = [root, parent, child];
                    budget_needed |= hierarchy
                        .iter()
                        .any(|scope| scope.limits.tokens().iter().any(Option::is_some));
                    accounts.push((key.workspace_id, hierarchy));
                }
            }
            let budget = budget_needed
                .then(|| {
                    Budget::open(
                        &configuration.state_directory,
                        tenant.id,
                        clock,
                        spool.state_lock(),
                        configuration.limits.active_requests as usize,
                    )
                })
                .transpose()?;
            let tenant = Arc::new(Tenant {
                traffic: Mutex::new((0..slots).map(|_| Traffic::default()).collect()),
                budget,
            });
            for (workspace, hierarchy) in accounts {
                keys.insert(
                    hierarchy[2].id,
                    Account {
                        workspace,
                        tenant: tenant.clone(),
                        hierarchy,
                    },
                );
            }
        }
        Ok(Some(Self { keys }))
    }

    /// The workspace and credential must have passed control authorization first.
    pub(crate) fn acquire(&self, workspace: Uuid, credential: &str) -> Result<Permit, Failure> {
        let id = credential
            .strip_prefix("pk1.")
            .and_then(|value| value.split_once('.'))
            .and_then(|(id, _)| Uuid::parse_str(id).ok());
        let account = id
            .and_then(|id| self.keys.get(&id))
            .filter(|account| account.workspace == workspace)
            .ok_or("quota_scope_unconfigured")?;
        account.acquire(Instant::now)
    }
}

impl Account {
    fn acquire(&self, clock: impl FnOnce() -> Instant) -> Result<Permit, Failure> {
        let mut traffic = self
            .tenant
            .traffic
            .lock()
            .map_err(|_| "quota_state_unavailable")?;
        let now = clock();
        for (scope, profile) in Scope::ALL.into_iter().zip(self.hierarchy) {
            let state = &mut traffic[profile.slot];
            if let Some(limit) = profile.limits.requests_per_second {
                while state
                    .starts
                    .front()
                    .is_some_and(|start| now.duration_since(*start) >= RATE_WINDOW)
                {
                    state.starts.pop_front();
                }
                if state.starts.len() >= limit.get() as usize {
                    return Err(Failure::throttled("request_rate_exceeded", scope.name(), 1));
                }
            }
            if profile
                .limits
                .concurrent_requests
                .is_some_and(|limit| state.active >= limit.get())
            {
                return Err(Failure::throttled(
                    "concurrency_limit_exceeded",
                    scope.name(),
                    1,
                ));
            }
        }
        for profile in self.hierarchy {
            let state = &mut traffic[profile.slot];
            if profile.limits.requests_per_second.is_some() {
                state.starts.push_back(now);
            }
            if profile.limits.concurrent_requests.is_some() {
                state.active += 1;
            }
        }
        Ok(Permit {
            account: self.clone(),
            hold: None,
        })
    }
}

pub(crate) struct Permit {
    account: Account,
    hold: Option<Hold>,
}

impl Permit {
    pub(crate) async fn reserve(&mut self, estimate: u32) -> Result<(), Failure> {
        let Some(budget) = &self.account.tenant.budget else {
            return Ok(());
        };
        let hierarchy = self.account.hierarchy;
        if hierarchy
            .iter()
            .all(|profile| profile.limits.tokens().iter().all(Option::is_none))
        {
            return Ok(());
        }
        self.hold = Some(budget.reserve(hierarchy, u64::from(estimate)).await?);
        Ok(())
    }

    pub(crate) async fn settle(&mut self, actual: u32) -> ConfigResult<()> {
        let Some(hold) = self.hold.take() else {
            return Ok(());
        };
        hold.settle(u64::from(actual)).await
    }
}

impl Drop for Permit {
    fn drop(&mut self) {
        if let Ok(mut traffic) = self.account.tenant.traffic.lock() {
            for profile in self.account.hierarchy {
                if profile.limits.concurrent_requests.is_some() {
                    traffic[profile.slot].active -= 1;
                }
            }
        }
    }
}

#[cfg(test)]
mod tests;
