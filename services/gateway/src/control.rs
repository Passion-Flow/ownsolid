use crate::configuration::{
    Configuration, MAX_CONTEXT_TOKENS, MAX_REQUEST_SECONDS, Result, certificate_file, secret_file,
};
use crate::errors::Failure;
use crate::protocol::valid_alias;
use futures_util::StreamExt;
use reqwest::header::{AUTHORIZATION, HeaderMap, HeaderValue};
use serde::de::DeserializeOwned;
use serde::{Deserialize, Serialize};
use std::sync::Once;
use std::time::Duration;
use uuid::Uuid;

const MAX_MODEL_BINDINGS: usize = 16;
const MAX_CONTROL_RESPONSE_BYTES: usize = 262_144;
const MAX_MACHINE_CREDENTIAL_BYTES: usize = 512;
static TLS_PROVIDER: Once = Once::new();

pub fn initialize_tls_provider() {
    TLS_PROVIDER.call_once(|| {
        // The build pins the ring provider; an already initialized ring provider is reusable.
        let _ = rustls::crypto::ring::default_provider().install_default();
    });
}

#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AliasBinding {
    pub id: Uuid,
    pub name: String,
    pub created: u64,
    pub deployment_id: Uuid,
    pub deployment_revision: u64,
    pub backend_model: String,
}

impl AliasBinding {
    pub fn validate(&self) -> Result<()> {
        if self.id.is_nil()
            || self.deployment_id.is_nil()
            || self.deployment_revision == 0
            || !valid_alias(&self.name)
            || self.backend_model.is_empty()
            || self.backend_model.len() > 128
            || !self
                .backend_model
                .bytes()
                .all(|value| value.is_ascii_alphanumeric() || b"-_.:/".contains(&value))
        {
            return Err("gateway_unavailable");
        }
        Ok(())
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ModelsAuthorization {
    pub gateway_id: Uuid,
    pub node_id: Uuid,
    pub workspace_id: Uuid,
    pub models: Vec<AliasBinding>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Admission {
    pub request_id: Uuid,
    pub gateway_id: Uuid,
    pub node_id: Uuid,
    pub workspace_id: Uuid,
    pub binding: AliasBinding,
    pub maximum_context_tokens: u32,
    pub maximum_request_seconds: u64,
}

#[derive(Serialize)]
#[serde(deny_unknown_fields)]
pub struct AdmissionRequest<'a> {
    pub request_id: Uuid,
    pub api_key: &'a str,
    pub model: &'a str,
    pub maximum_output_tokens: u32,
    pub stream: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Envelope<T> {
    data: T,
}

pub(crate) enum Operation {
    Models,
    Admissions,
    Outcomes,
}
impl Operation {
    fn path(&self) -> &'static str {
        match self {
            Self::Models => "models",
            Self::Admissions => "admissions",
            Self::Outcomes => "outcomes",
        }
    }
}

pub struct Control {
    client: reqwest::Client,
    base: String,
    gateway_id: Uuid,
    node_id: Uuid,
}

impl Control {
    pub fn new(configuration: &Configuration) -> Result<Self> {
        configuration.validate()?;
        initialize_tls_provider();
        let certificate = certificate_file(&configuration.control_ca_file)?;
        let roots = reqwest::Certificate::from_pem_bundle(&certificate)
            .map_err(|_| "GATEWAY_CONTROL_CA_INVALID")?;
        if roots.is_empty() {
            return Err("GATEWAY_CONTROL_CA_INVALID");
        }
        let secret = secret_file(&configuration.gateway_credential_file)?;
        let token = std::str::from_utf8(&secret)
            .map_err(|_| "GATEWAY_CREDENTIAL_INVALID")?
            .trim_end_matches('\n');
        if token.is_empty()
            || token.len() > MAX_MACHINE_CREDENTIAL_BYTES
            || !token
                .bytes()
                .all(|value| value.is_ascii_alphanumeric() || b"-_.".contains(&value))
        {
            return Err("GATEWAY_CREDENTIAL_INVALID");
        }
        let mut authorization = HeaderValue::from_str(&format!("Bearer {token}"))
            .map_err(|_| "GATEWAY_CREDENTIAL_INVALID")?;
        authorization.set_sensitive(true);
        let mut headers = HeaderMap::new();
        headers.insert(AUTHORIZATION, authorization);
        let client = reqwest::Client::builder()
            .tls_certs_only(roots)
            .default_headers(headers)
            .http1_only()
            .no_proxy()
            .redirect(reqwest::redirect::Policy::none())
            .retry(reqwest::retry::never())
            .connect_timeout(Duration::from_secs(configuration.limits.connect_seconds))
            .read_timeout(Duration::from_secs(configuration.limits.control_seconds))
            .timeout(Duration::from_secs(configuration.limits.control_seconds))
            .pool_max_idle_per_host(configuration.limits.control_idle_connections)
            .build()
            .map_err(|_| "GATEWAY_CONTROL_CLIENT_INVALID")?;
        Ok(Self {
            client,
            base: format!(
                "{}/gateway/v1/sites/{}/cells/{}",
                configuration.control_origin, configuration.site_id, configuration.cell_id
            ),
            gateway_id: configuration.gateway_id,
            node_id: configuration.node_id,
        })
    }

    pub(crate) async fn post<T: DeserializeOwned>(
        &self,
        operation: Operation,
        payload: &impl Serialize,
    ) -> std::result::Result<T, Failure> {
        let url = format!(
            "{}/instances/{}/{}",
            self.base,
            self.gateway_id,
            operation.path()
        );
        let response = self
            .client
            .post(url)
            .json(payload)
            .send()
            .await
            .map_err(|_| "gateway_unavailable")?;
        let status = response.status();
        let retry_after = response
            .headers()
            .get(reqwest::header::RETRY_AFTER)
            .and_then(|header| header.to_str().ok())
            .filter(|value| value.bytes().all(|byte| byte.is_ascii_digit()))
            .and_then(|value| value.parse::<std::num::NonZeroU64>().ok());
        if status == reqwest::StatusCode::OK
            && response
                .headers()
                .get(reqwest::header::CONTENT_TYPE)
                .and_then(|value| value.to_str().ok())
                .map(|value| value.split(';').next().unwrap_or("").trim())
                != Some("application/json")
        {
            return Err("gateway_unavailable".into());
        }
        if response
            .content_length()
            .is_some_and(|length| length > MAX_CONTROL_RESPONSE_BYTES as u64)
        {
            return Err("gateway_unavailable".into());
        }
        let mut stream = response.bytes_stream();
        let mut bytes = Vec::new();
        while let Some(chunk) = stream.next().await {
            let chunk = chunk.map_err(|_| "gateway_unavailable")?;
            if bytes.len() + chunk.len() > MAX_CONTROL_RESPONSE_BYTES {
                return Err("gateway_unavailable".into());
            }
            bytes.extend_from_slice(&chunk);
        }
        if status != reqwest::StatusCode::OK {
            #[derive(Deserialize)]
            struct Problem {
                code: String,
            }
            let code = serde_json::from_slice::<Problem>(&bytes)
                .map(|failure| failure.code)
                .unwrap_or_default();
            if matches!(operation, Operation::Outcomes) {
                match (status.as_u16(), code.as_str()) {
                    (400, "VALIDATION_FAILED" | "GATEWAY_USAGE_INVALID")
                    | (404, "GATEWAY_REQUEST_NOT_FOUND")
                    | (409, "GATEWAY_OUTCOME_CONFLICT") => {
                        return Err("GATEWAY_OUTCOME_REJECTED".into());
                    }
                    (401, "GATEWAY_UNAUTHORIZED") => {
                        return Err("GATEWAY_OUTCOME_AUTHORITY_INVALIDATED".into());
                    }
                    _ => {}
                }
            }
            if status.as_u16() == 429 && code == "RATE_LIMITED" {
                let mut failure = Failure::from("rate_limit_exceeded");
                if let Some(delay) = retry_after {
                    failure.retry_after = Some(delay.get());
                }
                return Err(failure);
            }
            return Err(Failure::from(match (status.as_u16(), code.as_str()) {
                (401, "API_KEY_INVALID") => "invalid_api_key",
                (401, "API_KEY_EXPIRED") => "key_expired",
                (403, "FORBIDDEN") => "permission_denied",
                (403, "LICENSE_NEW_BUSINESS_UNAVAILABLE") => "license_unavailable",
                (404, "MODEL_ALIAS_NOT_FOUND") => "model_not_found",
                (409, "MODEL_ALIAS_UNAVAILABLE") => "model_unavailable",
                _ => "gateway_unavailable",
            }));
        }
        serde_json::from_slice::<Envelope<T>>(&bytes)
            .map(|envelope| envelope.data)
            .map_err(|_| Failure::from("gateway_unavailable"))
    }

    pub async fn models(&self, api_key: &str) -> Result<ModelsAuthorization> {
        self.models_with_retry(api_key)
            .await
            .map_err(|error| error.code)
    }

    pub(crate) async fn models_with_retry(
        &self,
        api_key: &str,
    ) -> std::result::Result<ModelsAuthorization, Failure> {
        #[derive(Serialize)]
        struct ModelsRequest<'a> {
            api_key: &'a str,
        }
        let value: ModelsAuthorization = self
            .post(Operation::Models, &ModelsRequest { api_key })
            .await?;
        if value.gateway_id != self.gateway_id
            || value.node_id != self.node_id
            || value.workspace_id.is_nil()
            || value.models.len() > MAX_MODEL_BINDINGS
        {
            return Err("gateway_unavailable".into());
        }
        let mut names = std::collections::HashSet::new();
        for binding in &value.models {
            binding.validate()?;
            if !names.insert(&binding.name) {
                return Err("gateway_unavailable".into());
            }
        }
        Ok(value)
    }

    pub async fn admit(&self, request: &AdmissionRequest<'_>) -> Result<Admission> {
        self.admit_with_retry(request)
            .await
            .map_err(|error| error.code)
    }

    pub(crate) async fn admit_with_retry(
        &self,
        request: &AdmissionRequest<'_>,
    ) -> std::result::Result<Admission, Failure> {
        let value: Admission = self.post(Operation::Admissions, request).await?;
        if value.gateway_id != self.gateway_id
            || value.node_id != self.node_id
            || value.workspace_id.is_nil()
            || value.request_id != request.request_id
            || value.binding.name != request.model
            || !(1..=MAX_CONTEXT_TOKENS).contains(&value.maximum_context_tokens)
            || !(1..=MAX_REQUEST_SECONDS).contains(&value.maximum_request_seconds)
        {
            return Err("gateway_unavailable".into());
        }
        value.binding.validate()?;
        Ok(value)
    }
    pub async fn deliver_outcome(&self, outcome: &crate::outcome::Outcome) -> Result<()> {
        outcome.validate()?;
        let acknowledgment: crate::outcome::Acknowledgment = self
            .post(Operation::Outcomes, outcome)
            .await
            .map_err(|error| error.code)?;
        if acknowledgment.request_id != outcome.request_id
            || acknowledgment.canonical_sha256 != outcome.digest()?
        {
            return Err("GATEWAY_OUTCOME_ACKNOWLEDGMENT_INVALID");
        }
        Ok(())
    }
}
