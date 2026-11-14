use super::{
    Command, Denial, Grant, INVALID, MAX_LEASE_SECONDS, MAX_MESSAGE_BYTES, PATH, Settings,
    UNAVAILABLE, credential, token,
};
use crate::configuration::{
    Configuration, MAX_CONTEXT_TOKENS, Result as ConfigResult, private_directory, secret_file,
};
use crate::errors::Failure;
use crate::quota::{Permit, Quotas};
use crate::storage::{new_private_file, sync_directory};
use axum::body::to_bytes;
use axum::extract::{Request, State};
use axum::http::{HeaderMap, StatusCode, header};
use axum::response::{IntoResponse, Response};
use axum::{Json, Router, routing::post};
use sha2::{Digest, Sha256};
use std::collections::{BTreeSet, HashMap, HashSet};
use std::fs;
use std::io::Write;
use std::os::unix::fs::DirBuilderExt;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;
use subtle::ConstantTimeEq;
use tokio::time::Instant;
use uuid::Uuid;

const MARKER: &[u8] = b"gateway-quota-authority-v1\n";

struct Identity {
    workspace_id: Uuid,
    digest: [u8; 32],
}

struct Work {
    permit: Option<Permit>,
    reservation_attempted: bool,
    estimate: Option<u32>,
}

struct Lease {
    gateway_id: Uuid,
    expires: Instant,
    work: tokio::sync::Mutex<Work>,
}

#[derive(Default)]
struct Leases {
    active: HashMap<Uuid, Arc<Lease>>,
    expirations: BTreeSet<(Instant, Uuid)>,
}

impl Leases {
    fn prune(&mut self, now: Instant) {
        while self
            .expirations
            .first()
            .is_some_and(|(expires, _)| *expires <= now)
        {
            let (_, id) = self.expirations.pop_first().expect("a lease expires");
            self.active.remove(&id);
        }
    }

    fn remove(&mut self, id: Uuid) {
        if let Some(lease) = self.active.remove(&id) {
            self.expirations.remove(&(lease.expires, id));
        }
    }
}

pub(crate) struct Authority {
    quotas: Arc<Quotas>,
    peers: HashMap<Uuid, Identity>,
    leases: Mutex<Leases>,
    stopped: AtomicBool,
    lease_seconds: u64,
    maximum_leases: usize,
    ready_at: Instant,
    body_timeout: Duration,
}

fn marker(root: &Path) -> PathBuf {
    root.join("quotas").join("authority-v1")
}

pub(crate) fn verify_role(configuration: &Configuration) -> ConfigResult<()> {
    if !matches!(configuration.shared_quota, Some(Settings::Authority { .. }))
        && marker(&configuration.state_directory)
            .try_exists()
            .map_err(|_| INVALID)?
    {
        return Err("GATEWAY_QUOTA_AUTHORITY_ROLE_REQUIRED");
    }
    Ok(())
}

fn restart_fence(root: &Path) -> ConfigResult<Duration> {
    let marker = marker(root);
    let directory = marker.parent().ok_or(INVALID)?;
    if !directory.try_exists().map_err(|_| INVALID)? {
        fs::DirBuilder::new()
            .mode(0o700)
            .create(directory)
            .map_err(|_| INVALID)?;
        sync_directory(root)?;
    }
    private_directory(directory)?;
    if marker.try_exists().map_err(|_| INVALID)? {
        if secret_file(&marker)? != MARKER {
            return Err(INVALID);
        }
        return Ok(Duration::from_secs(MAX_LEASE_SECONDS));
    }
    let mut file = new_private_file(&marker)?;
    file.write_all(MARKER)
        .and_then(|_| file.sync_all())
        .map_err(|_| INVALID)?;
    sync_directory(directory)?;
    Ok(Duration::ZERO)
}

impl Authority {
    pub(crate) fn open(
        configuration: &Configuration,
        quotas: Option<&Arc<Quotas>>,
    ) -> ConfigResult<Option<Arc<Self>>> {
        let Some(Settings::Authority {
            peers,
            lease_seconds,
            maximum_leases,
        }) = &configuration.shared_quota
        else {
            return Ok(None);
        };
        let quotas = quotas.ok_or(INVALID)?.clone();
        let mut digests = HashSet::new();
        let peers = peers
            .iter()
            .map(|peer| {
                let digest = Sha256::digest(credential(&peer.credential_file)?.as_bytes()).into();
                if !digests.insert(digest) {
                    return Err(INVALID);
                }
                Ok((
                    peer.gateway_id,
                    Identity {
                        workspace_id: peer.workspace_id,
                        digest,
                    },
                ))
            })
            .collect::<ConfigResult<HashMap<_, _>>>()?;
        let ready_at = Instant::now() + restart_fence(&configuration.state_directory)?;
        Ok(Some(Arc::new(Self {
            quotas,
            peers,
            leases: Mutex::new(Leases::default()),
            stopped: AtomicBool::new(false),
            lease_seconds: *lease_seconds,
            maximum_leases: *maximum_leases,
            ready_at,
            body_timeout: Duration::from_secs(configuration.limits.idle_seconds),
        })))
    }

    fn admit_after_restart(&self, now: Instant) -> Result<(), Failure> {
        if now < self.ready_at {
            let wait = (self.ready_at - now).as_nanos().div_ceil(1_000_000_000) as u64;
            return Err(Failure {
                code: UNAVAILABLE,
                scope: None,
                retry_after: Some(wait),
            });
        }
        Ok(())
    }

    pub(crate) fn ready(&self) -> bool {
        !self.stopped.load(Ordering::Acquire) && Instant::now() >= self.ready_at
    }

    pub(crate) fn stop(&self) {
        self.stopped.store(true, Ordering::Release);
    }

    pub(crate) fn prune(&self) -> Result<(), Failure> {
        let now = Instant::now();
        if self.stopped.load(Ordering::Acquire) {
            return Err(UNAVAILABLE.into());
        }
        self.admit_after_restart(now)?;
        self.leases.lock().map_err(|_| UNAVAILABLE)?.prune(now);
        Ok(())
    }

    fn identify(&self, headers: &HeaderMap) -> Option<(Uuid, Uuid)> {
        if headers.get_all(header::AUTHORIZATION).iter().count() != 1 {
            return None;
        }
        let value = headers
            .get(header::AUTHORIZATION)?
            .to_str()
            .ok()?
            .strip_prefix("Bearer q1.")?;
        let (id, secret) = value.split_once('.')?;
        let id = Uuid::parse_str(id).ok()?;
        let identity = self.peers.get(&id)?;
        let digest = Sha256::digest(token(secret).ok()?.as_bytes());
        bool::from(identity.digest.ct_eq(digest.as_slice())).then_some((id, identity.workspace_id))
    }

    async fn execute(
        &self,
        gateway_id: Uuid,
        workspace: Uuid,
        command: Command,
    ) -> Result<Option<Grant>, Failure> {
        match command {
            Command::Acquire {
                workspace_id,
                key_id,
            } => {
                if workspace != workspace_id {
                    return Err("quota_scope_unconfigured".into());
                }
                self.prune()?;
                let mut leases = self.leases.lock().map_err(|_| UNAVAILABLE)?;
                if leases.active.len() >= self.maximum_leases {
                    return Err(Failure {
                        code: UNAVAILABLE,
                        scope: None,
                        retry_after: Some(1),
                    });
                }
                let permit = self.quotas.acquire_key(workspace, key_id)?;
                let id = Uuid::new_v4();
                let expires = Instant::now() + Duration::from_secs(self.lease_seconds);
                leases.expirations.insert((expires, id));
                leases.active.insert(
                    id,
                    Arc::new(Lease {
                        gateway_id,
                        expires,
                        work: tokio::sync::Mutex::new(Work {
                            permit: Some(permit),
                            reservation_attempted: false,
                            estimate: None,
                        }),
                    }),
                );
                Ok(Some(Grant {
                    lease_id: id,
                    lease_seconds: self.lease_seconds,
                }))
            }
            Command::Reserve { lease_id, estimate } => {
                if !(1..=MAX_CONTEXT_TOKENS).contains(&estimate) {
                    return Err("invalid_request_error".into());
                }
                let lease = self.lease(gateway_id, lease_id)?;
                let mut work = lease.work.lock().await;
                if work.reservation_attempted
                    || lease.expires <= Instant::now()
                    || self.stopped.load(Ordering::Acquire)
                {
                    return Err(UNAVAILABLE.into());
                }
                work.reservation_attempted = true;
                work.permit
                    .as_mut()
                    .ok_or(UNAVAILABLE)?
                    .reserve(estimate)
                    .await?;
                work.estimate = Some(estimate);
                Ok(None)
            }
            Command::Finish { lease_id, actual } => {
                let lease = self.lease(gateway_id, lease_id)?;
                let mut work = lease.work.lock().await;
                if actual.is_some_and(|used| {
                    used == 0 || work.estimate.is_none_or(|estimate| used > estimate)
                }) {
                    return Err("invalid_request_error".into());
                }
                let mut permit = work.permit.take().ok_or(UNAVAILABLE)?;
                self.leases
                    .lock()
                    .map_err(|_| UNAVAILABLE)?
                    .remove(lease_id);
                drop(work);
                let result = if let Some(used) = actual {
                    permit.settle(used).await
                } else {
                    Ok(())
                };
                result?;
                Ok(None)
            }
        }
    }

    fn lease(&self, gateway_id: Uuid, id: Uuid) -> Result<Arc<Lease>, Failure> {
        let leases = self.leases.lock().map_err(|_| UNAVAILABLE)?;
        leases
            .active
            .get(&id)
            .filter(|lease| lease.gateway_id == gateway_id && lease.expires > Instant::now())
            .cloned()
            .ok_or_else(|| "quota_lease_unavailable".into())
    }

    pub(crate) fn router<S: Clone + Send + Sync + 'static>(self: Arc<Self>) -> Router<S> {
        Router::new().route(PATH, post(handle)).with_state(self)
    }
}

async fn handle(State(authority): State<Arc<Authority>>, request: Request) -> Response {
    let Some((gateway_id, workspace)) = authority.identify(request.headers()) else {
        return StatusCode::UNAUTHORIZED.into_response();
    };
    if request
        .headers()
        .get(header::CONTENT_TYPE)
        .and_then(|value| value.to_str().ok())
        != Some("application/json")
    {
        return StatusCode::UNSUPPORTED_MEDIA_TYPE.into_response();
    }
    let bytes = match tokio::time::timeout(
        authority.body_timeout,
        to_bytes(request.into_body(), MAX_MESSAGE_BYTES),
    )
    .await
    {
        Ok(Ok(bytes)) => bytes,
        _ => return StatusCode::BAD_REQUEST.into_response(),
    };
    let Ok(command) = serde_json::from_slice(&bytes) else {
        return StatusCode::BAD_REQUEST.into_response();
    };
    match authority.execute(gateway_id, workspace, command).await {
        Ok(grant) => Json(grant).into_response(),
        Err(error) => {
            let status = match error.code {
                "quota_scope_unconfigured" => StatusCode::FORBIDDEN,
                "quota_lease_unavailable" => StatusCode::NOT_FOUND,
                "invalid_request_error" => StatusCode::BAD_REQUEST,
                _ if error.scope.is_some() => StatusCode::TOO_MANY_REQUESTS,
                _ => StatusCode::SERVICE_UNAVAILABLE,
            };
            let mut response = (
                status,
                Json(Denial {
                    code: error.code.into(),
                    scope: error.scope.map(str::to_owned),
                    retry_after: error.retry_after,
                }),
            )
                .into_response();
            if let Some(delay) = error.retry_after {
                response
                    .headers_mut()
                    .insert(header::RETRY_AFTER, delay.into());
            }
            response
        }
    }
}

#[cfg(test)]
mod tests;
