use super::{
    Command, Denial, Grant, INVALID, LEASE_GUARD_SECONDS, MAX_LEASE_SECONDS, MAX_MESSAGE_BYTES,
    PATH, Settings, UNAVAILABLE, credential,
};
use crate::configuration::{Configuration, Result as ConfigResult, certificate_file};
use crate::control::initialize_tls_provider;
use crate::errors::Failure;
use crate::quota::credential_key;
use axum::http::{HeaderMap, HeaderValue, StatusCode, header};
use futures_util::StreamExt;
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::Semaphore;
use tokio::time::Instant;
use uuid::Uuid;

pub(crate) struct Client {
    endpoint: String,
    http: reqwest::Client,
    releases: Arc<Semaphore>,
}

impl Client {
    pub(crate) fn open(configuration: &Configuration) -> ConfigResult<Option<Arc<Self>>> {
        let Some(Settings::Client {
            origin,
            ca_file,
            credential_file,
            timeout_seconds,
        }) = &configuration.shared_quota
        else {
            return Ok(None);
        };
        initialize_tls_provider();
        let roots = reqwest::Certificate::from_pem_bundle(&certificate_file(ca_file)?)
            .map_err(|_| INVALID)?;
        if roots.is_empty() {
            return Err(INVALID);
        }
        let secret = credential(credential_file)?;
        let mut auth =
            HeaderValue::from_str(&format!("Bearer q1.{}.{secret}", configuration.gateway_id))
                .map_err(|_| INVALID)?;
        auth.set_sensitive(true);
        let mut headers = HeaderMap::new();
        headers.insert(header::AUTHORIZATION, auth);
        let http = reqwest::Client::builder()
            .tls_certs_only(roots)
            .default_headers(headers)
            .http1_only()
            .no_proxy()
            .redirect(reqwest::redirect::Policy::none())
            .retry(reqwest::retry::never())
            .timeout(Duration::from_secs(*timeout_seconds))
            .pool_max_idle_per_host(configuration.limits.control_idle_connections)
            .build()
            .map_err(|_| INVALID)?;
        Ok(Some(Arc::new(Self {
            endpoint: format!("{origin}{PATH}"),
            http,
            releases: Arc::new(Semaphore::new(
                configuration.limits.maximum_connections as usize,
            )),
        })))
    }

    async fn call(&self, command: Command) -> Result<Option<Grant>, Failure> {
        let response = self
            .http
            .post(&self.endpoint)
            .json(&command)
            .send()
            .await
            .map_err(|_| UNAVAILABLE)?;
        let status = response.status();
        if response
            .headers()
            .get(header::CONTENT_TYPE)
            .and_then(|value| value.to_str().ok())
            != Some("application/json")
        {
            return Err(UNAVAILABLE.into());
        }
        if response
            .content_length()
            .is_some_and(|size| size > MAX_MESSAGE_BYTES as u64)
        {
            return Err(UNAVAILABLE.into());
        }
        let mut stream = response.bytes_stream();
        let mut bytes = Vec::new();
        while let Some(chunk) = stream.next().await {
            let chunk = chunk.map_err(|_| UNAVAILABLE)?;
            if bytes.len() + chunk.len() > MAX_MESSAGE_BYTES {
                return Err(UNAVAILABLE.into());
            }
            bytes.extend_from_slice(&chunk);
        }
        if status == StatusCode::OK {
            serde_json::from_slice(&bytes).map_err(|_| UNAVAILABLE.into())
        } else {
            Err(serde_json::from_slice::<Denial>(&bytes)
                .map_err(|_| UNAVAILABLE)?
                .failure(status))
        }
    }

    pub(crate) async fn acquire(
        self: &Arc<Self>,
        workspace_id: Uuid,
        credential: &str,
    ) -> Result<RemotePermit, Failure> {
        let key_id = credential_key(credential).ok_or("quota_scope_unconfigured")?;
        let started = Instant::now();
        let grant = self
            .call(Command::Acquire {
                workspace_id,
                key_id,
            })
            .await?
            .ok_or(UNAVAILABLE)?;
        if grant.lease_id.is_nil() || !(2..=MAX_LEASE_SECONDS).contains(&grant.lease_seconds) {
            return Err(UNAVAILABLE.into());
        }
        let permit = RemotePermit {
            client: self.clone(),
            id: Some(grant.lease_id),
            deadline: started + Duration::from_secs(grant.lease_seconds - LEASE_GUARD_SECONDS),
        };
        if permit.deadline <= Instant::now() {
            return Err(UNAVAILABLE.into());
        }
        Ok(permit)
    }
}

pub(crate) struct RemotePermit {
    client: Arc<Client>,
    id: Option<Uuid>,
    pub(crate) deadline: Instant,
}

impl RemotePermit {
    pub(crate) async fn reserve(&self, estimate: u32) -> Result<(), Failure> {
        let response = self
            .client
            .call(Command::Reserve {
                lease_id: self.id.ok_or(UNAVAILABLE)?,
                estimate,
            })
            .await?;
        if response.is_some() {
            return Err(UNAVAILABLE.into());
        }
        Ok(())
    }

    pub(crate) async fn settle(&mut self, actual: u32) -> ConfigResult<()> {
        let id = self.id.take().ok_or(UNAVAILABLE)?;
        self.client
            .call(Command::Finish {
                lease_id: id,
                actual: Some(actual),
            })
            .await
            .map_err(|error| error.code)?;
        Ok(())
    }
}

impl Drop for RemotePermit {
    fn drop(&mut self) {
        if let Some(lease_id) = self.id.take()
            && let Ok(slot) = self.client.releases.clone().try_acquire_owned()
            && let Ok(runtime) = tokio::runtime::Handle::try_current()
        {
            let client = self.client.clone();
            runtime.spawn(async move {
                let _slot = slot;
                let _ = client
                    .call(Command::Finish {
                        lease_id,
                        actual: None,
                    })
                    .await;
            });
        }
    }
}
