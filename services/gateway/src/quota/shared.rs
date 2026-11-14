//! Versioned, TLS-only protocol for one shared quota authority.
mod authority;
mod client;
pub(crate) use authority::{Authority, verify_role};
pub(crate) use client::{Client, RemotePermit};

use crate::configuration::{
    Configuration, MAX_REQUEST_SECONDS, Result as ConfigResult, secret_file,
};
use crate::errors::Failure;
use axum::http::StatusCode;
use serde::{Deserialize, Serialize};
use std::collections::HashSet;
use std::path::{Path, PathBuf};
use uuid::Uuid;

const UNAVAILABLE: &str = "quota_state_unavailable";
const INVALID: &str = "GATEWAY_SHARED_QUOTA_CONFIGURATION_INVALID";
const PATH: &str = "/internal/v1/quota";
const MAX_MESSAGE_BYTES: usize = 4096;
const MAX_LEASE_SECONDS: u64 = MAX_REQUEST_SECONDS + 60;
const LEASE_GUARD_SECONDS: u64 = 1;

#[derive(Clone, Deserialize, Serialize)]
#[serde(tag = "mode", rename_all = "snake_case", deny_unknown_fields)]
pub(crate) enum Settings {
    Authority {
        peers: Vec<Peer>,
        lease_seconds: u64,
        maximum_leases: usize,
    },
    Client {
        origin: String,
        ca_file: PathBuf,
        credential_file: PathBuf,
        timeout_seconds: u64,
    },
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct Peer {
    gateway_id: Uuid,
    workspace_id: Uuid,
    credential_file: PathBuf,
}

impl Settings {
    pub(crate) fn validate(&self, configuration: &Configuration) -> ConfigResult<()> {
        match self {
            Self::Authority {
                peers,
                lease_seconds,
                maximum_leases,
            } => {
                let address = configuration.listen_address.ip();
                if configuration.quota_policy.is_none()
                    || !(address.is_loopback() || address.is_private())
                    || peers.is_empty()
                    || !(2..=MAX_LEASE_SECONDS).contains(lease_seconds)
                    || !(1..=4096).contains(maximum_leases)
                {
                    return Err(INVALID);
                }
                let mut ids = HashSet::new();
                for peer in peers {
                    if peer.gateway_id.is_nil()
                        || peer.gateway_id == configuration.gateway_id
                        || peer.workspace_id.is_nil()
                        || peer.credential_file == configuration.gateway_credential_file
                        || !ids.insert(peer.gateway_id)
                    {
                        return Err(INVALID);
                    }
                }
            }
            Self::Client {
                origin,
                credential_file,
                timeout_seconds,
                ..
            } => {
                let url = reqwest::Url::parse(origin).map_err(|_| INVALID)?;
                if configuration.quota_policy.is_some()
                    || credential_file == &configuration.gateway_credential_file
                    || !(1..=30).contains(timeout_seconds)
                    || url.scheme() != "https"
                    || url.host_str().is_none()
                    || !url.username().is_empty()
                    || url.password().is_some()
                    || url.path() != "/"
                    || url.query().is_some()
                    || url.fragment().is_some()
                    || origin.ends_with('/')
                {
                    return Err(INVALID);
                }
            }
        }
        Ok(())
    }
}

fn token(value: &str) -> ConfigResult<&str> {
    if value.len() != 64
        || !value
            .bytes()
            .all(|b| b.is_ascii_hexdigit() && !b.is_ascii_uppercase())
    {
        return Err(INVALID);
    }
    Ok(value)
}

fn credential(path: &Path) -> ConfigResult<String> {
    let contents = String::from_utf8(secret_file(path)?).map_err(|_| INVALID)?;
    Ok(token(contents.strip_suffix('\n').unwrap_or(&contents))?.to_owned())
}

#[derive(Serialize, Deserialize)]
#[serde(tag = "operation", rename_all = "snake_case", deny_unknown_fields)]
enum Command {
    Acquire { workspace_id: Uuid, key_id: Uuid },
    Reserve { lease_id: Uuid, estimate: u32 },
    Finish { lease_id: Uuid, actual: Option<u32> },
}

#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct Grant {
    lease_id: Uuid,
    lease_seconds: u64,
}

#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct Denial {
    code: String,
    scope: Option<String>,
    retry_after: Option<u64>,
}

impl Denial {
    fn failure(self, status: StatusCode) -> Failure {
        let code = match (status, self.code.as_str()) {
            (StatusCode::TOO_MANY_REQUESTS, "request_rate_exceeded") => "request_rate_exceeded",
            (StatusCode::TOO_MANY_REQUESTS, "concurrency_limit_exceeded") => {
                "concurrency_limit_exceeded"
            }
            (StatusCode::TOO_MANY_REQUESTS, "hourly_token_budget_exceeded") => {
                "hourly_token_budget_exceeded"
            }
            (StatusCode::TOO_MANY_REQUESTS, "daily_token_budget_exceeded") => {
                "daily_token_budget_exceeded"
            }
            (StatusCode::TOO_MANY_REQUESTS, "monthly_token_budget_exceeded") => {
                "monthly_token_budget_exceeded"
            }
            (StatusCode::FORBIDDEN, "quota_scope_unconfigured") => "quota_scope_unconfigured",
            _ => UNAVAILABLE,
        };
        let scope = match self
            .scope
            .as_deref()
            .filter(|_| status == StatusCode::TOO_MANY_REQUESTS && code != UNAVAILABLE)
        {
            Some("tenant") => Some("tenant"),
            Some("application") => Some("application"),
            Some("key") => Some("key"),
            _ => None,
        };
        let retry_after = self
            .retry_after
            .filter(|delay| (1..=31 * 24 * 3600).contains(delay));
        if status == StatusCode::TOO_MANY_REQUESTS && (scope.is_none() || retry_after.is_none()) {
            return UNAVAILABLE.into();
        }
        Failure {
            code,
            scope,
            retry_after,
        }
    }
}
